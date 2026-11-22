package money

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	contract "github.com/opentrawl/opentrawl/trawlers/money/proto/trawl/money"
	"github.com/opentrawl/opentrawl/trawlkit"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const moneySchema = `CREATE TABLE IF NOT EXISTS money_accounts(reference TEXT PRIMARY KEY, record BLOB NOT NULL, read_time TEXT NOT NULL); CREATE TABLE IF NOT EXISTS money_movements(reference TEXT PRIMARY KEY,account_reference TEXT NOT NULL,pending INTEGER NOT NULL,record BLOB NOT NULL,provider TEXT NOT NULL,read_time TEXT NOT NULL); CREATE VIRTUAL TABLE IF NOT EXISTS money_search USING fts5(reference UNINDEXED,field_label UNINDEXED,body); CREATE TABLE IF NOT EXISTS money_update_completion(singleton INTEGER PRIMARY KEY,completed_time TEXT NOT NULL); CREATE TABLE IF NOT EXISTS money_imports(name TEXT PRIMARY KEY); CREATE TABLE IF NOT EXISTS money_enable_banking_refresh(connection_identifier TEXT PRIMARY KEY,attempted_time TEXT NOT NULL);`

type storedMovement struct {
	accountReference string
	booked           *contract.MoneyMovement
	pending          *contract.MoneyPendingMovement
	provider         string
	readTime         *timestamppb.Timestamp
}

func (movement storedMovement) date(basis string) string {
	if movement.pending != nil {
		return calendarDateText(movement.pending.ChargeDate)
	}
	if basis == "charged" {
		return calendarDateText(movement.booked.ChargeDate)
	}
	date := calendarDateText(movement.booked.BookingDate)
	if date == "" {
		date = calendarDateText(movement.booked.ChargeDate)
	}
	return date
}
func (movement storedMovement) reference() string {
	if movement.pending != nil {
		return trawlkit.CanonicalArchiveRecordReferenceText(movement.pending.CanonicalRecordReference)
	}
	return trawlkit.CanonicalArchiveRecordReferenceText(movement.booked.CanonicalRecordReference)
}
func readAccounts(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) ([]*contract.MoneyAccount, error) {
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, "SELECT record FROM money_accounts ORDER BY reference")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var accounts []*contract.MoneyAccount
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		account := &contract.MoneyAccount{}
		if err := proto.Unmarshal(encoded, account); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}
func readMovements(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, pending bool) ([]storedMovement, error) {
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, "SELECT account_reference,record,provider,read_time FROM money_movements WHERE pending=? ORDER BY reference", pending)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var movements []storedMovement
	for rows.Next() {
		var encoded []byte
		var stamp string
		movement := storedMovement{}
		if err := rows.Scan(&movement.accountReference, &encoded, &movement.provider, &stamp); err != nil {
			return nil, err
		}
		movement.readTime = &timestamppb.Timestamp{}
		if err := prototext.Unmarshal([]byte(stamp), movement.readTime); err != nil {
			return nil, err
		}
		var record proto.Message
		if pending {
			movement.pending = &contract.MoneyPendingMovement{}
			record = movement.pending
		} else {
			movement.booked = &contract.MoneyMovement{}
			record = movement.booked
		}
		if err := proto.Unmarshal(encoded, record); err != nil {
			return nil, err
		}
		movements = append(movements, movement)
	}
	return movements, rows.Err()
}
func stableReference(kind string, identifiers ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(identifiers, "\x00")))
	return "money:" + kind + "/" + hex.EncodeToString(digest[:])
}
func replacePopulatedFields(previous, current proto.Message) {
	current.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		previous.ProtoReflect().Set(field, value)
		return true
	})
}
func (*App) applyCompletedImports(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error) {
	db := req.OpenedTrawlerArchiveStore.DB()
	if _, err := db.ExecContext(ctx, moneySchema); err != nil {
		return nil, err
	}
	importsDirectory := filepath.Join(filepath.Dir(req.TrawlerArchivePaths.TrawlerArchivePath), "imports")
	if err := os.MkdirAll(filepath.Join(importsDirectory, "applied"), 0700); err != nil {
		return nil, err
	}

	type completedImport struct {
		name          string
		directoryPath string
		retained      bool
		record        *contract.MoneyImport
	}
	var imports []completedImport
	var failures []error
	for _, base := range []string{importsDirectory, filepath.Join(importsDirectory, "applied")} {
		directories, err := os.ReadDir(base)
		if err != nil {
			return nil, err
		}
		for _, directory := range directories {
			if !directory.IsDir() || directory.Name() == "applied" || strings.HasPrefix(directory.Name(), ".") {
				continue
			}
			retained := base != importsDirectory
			if retained {
				var applied int
				err := db.QueryRowContext(ctx, "SELECT 1 FROM money_imports WHERE name=?", directory.Name()).Scan(&applied)
				if err == nil {
					continue
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
			}
			path := filepath.Join(base, directory.Name())
			encoded, err := os.ReadFile(filepath.Join(path, "import.txtpb"))
			if err != nil {
				failures = append(failures, fmt.Errorf("read import %s: %w", directory.Name(), err))
				continue
			}
			record := &contract.MoneyImport{}
			if err := prototext.Unmarshal(encoded, record); err != nil {
				failures = append(failures, fmt.Errorf("parse import %s: %w", directory.Name(), err))
				continue
			}
			if record.ProviderReadTime == nil || record.ProviderReadTime.CheckValid() != nil || record.ProviderDisplayName == "" {
				failures = append(failures, fmt.Errorf("import %s requires provider and read time", directory.Name()))
				continue
			}
			imports = append(imports, completedImport{name: directory.Name(), directoryPath: path, retained: retained, record: record})
		}
	}
	sort.Slice(imports, func(i, j int) bool {
		left, right := imports[i], imports[j]
		if left.record.ProviderReadTime.AsTime().Equal(right.record.ProviderReadTime.AsTime()) {
			return left.name < right.name
		}
		return left.record.ProviderReadTime.AsTime().Before(right.record.ProviderReadTime.AsTime())
	})
	var added, changed, removed uint64
	for _, entry := range imports {
		newCount, changedCount, removedCount, err := applyImport(ctx, db, entry.name, entry.record)
		if err != nil {
			failures = append(failures, fmt.Errorf("apply import %s: %w", entry.name, err))
			continue
		}
		added += newCount
		changed += changedCount
		removed += removedCount
		if entry.retained {
			continue
		}
		if err := os.Rename(entry.directoryPath, filepath.Join(importsDirectory, "applied", entry.name)); err != nil {
			failures = append(failures, err)
		}
	}
	return &update.TrawlerArchiveUpdateReport{ArchiveRecordCountAddedByThisUpdate: proto.Uint64(added), ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(changed), ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(removed)}, errors.Join(failures...)
}
func applyImport(ctx context.Context, db *sql.DB, name string, imported *contract.MoneyImport) (added, changed, removed uint64, returnedError error) {
	if err := validateMoneyImport(imported); err != nil {
		return 0, 0, 0, err
	}
	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = transaction.Rollback() }()
	alreadyApplied, err := isMoneyImportApplied(ctx, transaction, name)
	if err != nil || alreadyApplied {
		return 0, 0, 0, err
	}
	importCtx := moneyImportWriteContext{
		imported:   imported,
		stamp:      imported.ProviderReadTime.AsTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		stampProto: prototext.Format(imported.ProviderReadTime),
	}
	for _, accountImport := range imported.MoneyAccountImports {
		a, c, r, err := applyAccountImport(ctx, transaction, importCtx, accountImport)
		if err != nil {
			return 0, 0, 0, err
		}
		added += a
		changed += c
		removed += r
	}
	if _, err := transaction.ExecContext(ctx, "INSERT INTO money_imports VALUES(?)", name); err != nil {
		return 0, 0, 0, err
	}
	return added, changed, removed, transaction.Commit()
}

type moneyImportWriteContext struct {
	imported   *contract.MoneyImport
	stamp      string
	stampProto string
}

type indexedMoneyField struct {
	label string
	text  string
}

func isMoneyImportApplied(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var applied int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM money_imports WHERE name=?", name).Scan(&applied)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func applyAccountImport(
	ctx context.Context,
	tx *sql.Tx,
	importCtx moneyImportWriteContext,
	accountImport *contract.MoneyAccountImport,
) (added, changed, removed uint64, err error) {
	account := accountImport.MoneyAccount
	identity := account.GetMoneyAccountIdentifier()
	if identity.GetInstitutionIdentifier() == "" || identity.GetAccountIdentifierNamespace() == "" || identity.GetStableAccountIdentifier() == "" {
		return 0, 0, 0, fmt.Errorf("account requires stable institution, namespace and identity")
	}
	reference := stableReference("account", identity.InstitutionIdentifier, identity.AccountIdentifierNamespace, identity.StableAccountIdentifier)
	account.CanonicalRecordReference = trawlkit.NewCanonicalArchiveRecordReference(reference)

	previous, previousStamp, err := loadStoredMoneyAccount(ctx, tx, reference)
	if err != nil {
		return 0, 0, 0, err
	}
	oldPendingRecords := map[string][]byte{}
	if accountImport.CompletePendingMovementList != nil {
		oldPendingRecords, err = loadPendingMoneyMovements(ctx, tx, reference)
		if err != nil {
			return 0, 0, 0, err
		}
	}
	pendingIsNewer := previous.LastPendingMovementsReadTime == nil || !importCtx.imported.ProviderReadTime.AsTime().Before(previous.LastPendingMovementsReadTime.AsTime())
	if err := upsertMergedMoneyAccount(ctx, tx, importCtx, accountImport, reference, previous, previousStamp, pendingIsNewer); err != nil {
		return 0, 0, 0, err
	}
	movements := collectAccountImportMovements(reference, accountImport, pendingIsNewer)
	for _, movement := range movements {
		wasAdded, wasChanged, err := applyMovementImport(ctx, tx, importCtx, reference, movement, oldPendingRecords)
		if err != nil {
			return 0, 0, 0, err
		}
		if wasAdded {
			added++
		} else if wasChanged {
			changed++
		}
	}
	if accountImport.CompletePendingMovementList != nil && pendingIsNewer {
		removed += uint64(len(oldPendingRecords))
	}
	return added, changed, removed, nil
}

func loadStoredMoneyAccount(ctx context.Context, tx *sql.Tx, reference string) (*contract.MoneyAccount, string, error) {
	previous := &contract.MoneyAccount{}
	var previousEncoded []byte
	var previousStamp string
	err := tx.QueryRowContext(ctx, "SELECT record,read_time FROM money_accounts WHERE reference=?", reference).Scan(&previousEncoded, &previousStamp)
	if errors.Is(err, sql.ErrNoRows) {
		return previous, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if err := proto.Unmarshal(previousEncoded, previous); err != nil {
		return nil, "", err
	}
	return previous, previousStamp, nil
}

func loadPendingMoneyMovements(ctx context.Context, tx *sql.Tx, accountReference string) (map[string][]byte, error) {
	rows, err := tx.QueryContext(ctx, "SELECT reference,record FROM money_movements WHERE account_reference=? AND pending=1", accountReference)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	pending := map[string][]byte{}
	for rows.Next() {
		var ref string
		var encoded []byte
		if err := rows.Scan(&ref, &encoded); err != nil {
			return nil, err
		}
		pending[ref] = encoded
	}
	return pending, rows.Err()
}

func upsertMergedMoneyAccount(
	ctx context.Context,
	tx *sql.Tx,
	importCtx moneyImportWriteContext,
	accountImport *contract.MoneyAccountImport,
	reference string,
	previous *contract.MoneyAccount,
	previousStamp string,
	pendingIsNewer bool,
) error {
	if previousStamp <= importCtx.stamp {
		replacePopulatedFields(previous, accountImport.MoneyAccount)
	}
	if accountImport.BookedMovementList != nil && (previous.LastBookedMovementsReadTime == nil || !importCtx.imported.ProviderReadTime.AsTime().Before(previous.LastBookedMovementsReadTime.AsTime())) {
		previous.LastBookedMovementsReadTime = importCtx.imported.ProviderReadTime
	}
	if accountImport.CompletePendingMovementList != nil && pendingIsNewer {
		previous.LastPendingMovementsReadTime = importCtx.imported.ProviderReadTime
		if _, err := tx.ExecContext(ctx, "DELETE FROM money_search WHERE reference IN (SELECT reference FROM money_movements WHERE account_reference=? AND pending=1)", reference); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM money_movements WHERE account_reference=? AND pending=1", reference); err != nil {
			return err
		}
	}
	encoded, err := proto.Marshal(previous)
	if err != nil {
		return err
	}
	accountStamp := max(importCtx.stamp, previousStamp)
	_, err = tx.ExecContext(ctx, "INSERT INTO money_accounts VALUES(?,?,?) ON CONFLICT(reference) DO UPDATE SET record=excluded.record,read_time=excluded.read_time", reference, encoded, accountStamp)
	return err
}

func collectAccountImportMovements(accountReference string, accountImport *contract.MoneyAccountImport, includePending bool) []storedMovement {
	var movements []storedMovement
	for _, movement := range accountImport.GetBookedMovementList().GetBookedMovements() {
		movement.CanonicalRecordReference = trawlkit.NewCanonicalArchiveRecordReference(stableReference("movement", accountReference, movement.GetMoneyMovementIdentifier().GetMovementIdentifierNamespace(), movement.GetMoneyMovementIdentifier().GetAccountScopedMovementIdentifier()))
		movements = append(movements, storedMovement{booked: movement})
	}
	if !includePending {
		return movements
	}
	for _, movement := range accountImport.GetCompletePendingMovementList().GetPendingMovements() {
		movement.CanonicalRecordReference = trawlkit.NewCanonicalArchiveRecordReference(stableReference("pending", accountReference, movement.GetMoneyMovementIdentifier().GetMovementIdentifierNamespace(), movement.GetMoneyMovementIdentifier().GetAccountScopedMovementIdentifier()))
		movements = append(movements, storedMovement{pending: movement})
	}
	return movements
}

func applyMovementImport(
	ctx context.Context,
	tx *sql.Tx,
	importCtx moneyImportWriteContext,
	accountReference string,
	movement storedMovement,
	oldPendingRecords map[string][]byte,
) (wasAdded, wasChanged bool, err error) {
	record, identifier := movementRecordAndIdentifier(movement)
	if identifier.GetMovementIdentifierNamespace() == "" || identifier.GetAccountScopedMovementIdentifier() == "" {
		return false, false, fmt.Errorf("movement requires namespace and account-scoped identifier")
	}
	movementRef := movement.reference()
	old, oldStamp, found, err := loadExistingMovementBytes(ctx, tx, movement, movementRef, importCtx.stampProto, oldPendingRecords)
	if err != nil {
		return false, false, err
	}
	recordProvider := importCtx.imported.ProviderDisplayName
	recordReadTime := importCtx.stampProto
	if found {
		record, recordProvider, recordReadTime, err = mergeMovementWithStored(ctx, tx, importCtx, movementRef, record, old, oldStamp, recordProvider, recordReadTime)
		if err != nil {
			return false, false, err
		}
	}
	if err := replaceMovementSearchFields(ctx, tx, movementRef, movement.pending != nil, record); err != nil {
		return false, false, err
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(record)
	if err != nil {
		return false, false, err
	}
	wasAdded, wasChanged, err = classifyMovementDiff(old, record)
	if err != nil {
		return false, false, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO money_movements VALUES(?,?,?,?,?,?) ON CONFLICT(reference) DO UPDATE SET record=excluded.record,provider=excluded.provider,read_time=excluded.read_time", movementRef, accountReference, movement.pending != nil, encoded, recordProvider, recordReadTime)
	return wasAdded, wasChanged, err
}

func movementRecordAndIdentifier(movement storedMovement) (proto.Message, *contract.MoneyMovementIdentifier) {
	if movement.booked != nil {
		return movement.booked, movement.booked.MoneyMovementIdentifier
	}
	return movement.pending, movement.pending.MoneyMovementIdentifier
}

func loadExistingMovementBytes(
	ctx context.Context,
	tx *sql.Tx,
	movement storedMovement,
	movementRef string,
	stampProto string,
	oldPendingRecords map[string][]byte,
) ([]byte, string, bool, error) {
	var old []byte
	var oldStamp string
	err := tx.QueryRowContext(ctx, "SELECT record,read_time FROM money_movements WHERE reference=?", movementRef).Scan(&old, &oldStamp)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", false, err
	}
	found := err == nil
	if movement.pending != nil && !found {
		if prior := oldPendingRecords[movementRef]; prior != nil {
			old = prior
			oldStamp = stampProto
			found = true
		}
	}
	delete(oldPendingRecords, movementRef)
	return old, oldStamp, found, nil
}

func mergeMovementWithStored(
	ctx context.Context,
	tx *sql.Tx,
	importCtx moneyImportWriteContext,
	movementRef string,
	record proto.Message,
	old []byte,
	oldStamp string,
	recordProvider string,
	recordReadTime string,
) (proto.Message, string, string, error) {
	oldTime := &timestamppb.Timestamp{}
	if err := prototext.Unmarshal([]byte(oldStamp), oldTime); err != nil {
		return nil, "", "", err
	}
	merged := record.ProtoReflect().New().Interface()
	if err := proto.Unmarshal(old, merged); err != nil {
		return nil, "", "", err
	}
	if oldTime.AsTime().After(importCtx.imported.ProviderReadTime.AsTime()) {
		replacePopulatedFields(record, merged)
		var provider string
		if err := tx.QueryRowContext(ctx, "SELECT provider FROM money_movements WHERE reference=?", movementRef).Scan(&provider); err != nil {
			return nil, "", "", err
		}
		return record, provider, oldStamp, nil
	}
	replacePopulatedFields(merged, record)
	return merged, recordProvider, recordReadTime, nil
}

func replaceMovementSearchFields(ctx context.Context, tx *sql.Tx, movementRef string, isPending bool, record proto.Message) error {
	fields := indexedFieldsForMoneyRecord(isPending, record)
	if _, err := tx.ExecContext(ctx, "DELETE FROM money_search WHERE reference=?", movementRef); err != nil {
		return err
	}
	for _, field := range fields {
		if field.text == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO money_search(reference,field_label,body) VALUES(?,?,?)", movementRef, field.label, field.text); err != nil {
			return err
		}
	}
	return nil
}

func indexedFieldsForMoneyRecord(isPending bool, record proto.Message) []indexedMoneyField {
	if isPending {
		m := record.(*contract.MoneyPendingMovement)
		return []indexedMoneyField{
			{"Transaction", nonemptyJoined(m.CounterpartyDisplayName, m.MovementDescription)},
			{"Provider reference", m.GetMoneyMovementIdentifier().GetAccountScopedMovementIdentifier()},
		}
	}
	m := record.(*contract.MoneyMovement)
	fields := []indexedMoneyField{
		{"Transaction", nonemptyJoined(m.CounterpartyDisplayName, m.MovementDescription)},
		{"Reference", m.PaymentReference},
		{"Account", m.CounterpartyAccountIdentifier},
		{"Mandate", m.DirectDebitMandateIdentifier},
		{"Creditor", m.DirectDebitCreditorIdentifier},
		{"Card member", m.CardMemberDisplayName},
		{"Place", nonemptyJoined(m.MerchantCity, m.MerchantCountryName)},
		{"Details", strings.Join(m.AdditionalPaymentDetails, " ")},
		{"Provider reference", m.GetMoneyMovementIdentifier().GetAccountScopedMovementIdentifier()},
		{"Merchant category", m.MerchantCategoryCode},
		{"Bank transaction code", m.ProviderBankTransactionCode},
	}
	if flight := m.FlightTicket; flight != nil {
		fields = append(fields, indexedMoneyField{"Flight", nonemptyJoined(flight.DeparturePlace, flight.ArrivalPlace, flight.AirlineCode, flight.BookingClassCode, flight.TicketNumber, flight.PassengerName)})
	}
	return fields
}

func classifyMovementDiff(old []byte, record proto.Message) (added, changed bool, err error) {
	if old == nil {
		return true, false, nil
	}
	oldRecord := record.ProtoReflect().New().Interface()
	if err := proto.Unmarshal(old, oldRecord); err != nil {
		return false, false, err
	}
	return false, !proto.Equal(oldRecord, record), nil
}

func (app *App) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error) {
	first, firstError := app.applyCompletedImports(ctx, req)
	if firstError != nil {
		return first, firstError
	}
	if err := collectEnableBanking(ctx, req); err != nil {
		return first, err
	}
	second, secondError := app.applyCompletedImports(ctx, req)
	if second != nil {
		first.ArchiveRecordCountAddedByThisUpdate = proto.Uint64(first.GetArchiveRecordCountAddedByThisUpdate() + second.GetArchiveRecordCountAddedByThisUpdate())
		first.ArchiveRecordCountUpdatedByThisUpdate = proto.Uint64(first.GetArchiveRecordCountUpdatedByThisUpdate() + second.GetArchiveRecordCountUpdatedByThisUpdate())
		first.ArchiveRecordCountRemovedByThisUpdate = proto.Uint64(first.GetArchiveRecordCountRemovedByThisUpdate() + second.GetArchiveRecordCountRemovedByThisUpdate())
	}
	return first, secondError
}

func (*App) RecordSuccessfullyCompletedArchiveUpdate(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) error {
	_, err := req.OpenedTrawlerArchiveStore.DB().ExecContext(ctx, "INSERT INTO money_update_completion VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET completed_time=excluded.completed_time", prototext.Format(timestamppb.Now()))
	return err
}
