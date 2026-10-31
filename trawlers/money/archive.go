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

	"github.com/opentrawl/opentrawl/trawlkit"
	contract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/money"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const moneySchema = `CREATE TABLE IF NOT EXISTS money_accounts(reference TEXT PRIMARY KEY, record BLOB NOT NULL, read_time TEXT NOT NULL); CREATE TABLE IF NOT EXISTS money_movements(reference TEXT PRIMARY KEY,account_reference TEXT NOT NULL,pending INTEGER NOT NULL,record BLOB NOT NULL,provider TEXT NOT NULL,read_time TEXT NOT NULL); CREATE VIRTUAL TABLE IF NOT EXISTS money_search USING fts5(reference UNINDEXED,field_label UNINDEXED,body); CREATE TABLE IF NOT EXISTS money_update_completion(singleton INTEGER PRIMARY KEY,completed_time TEXT NOT NULL); CREATE TABLE IF NOT EXISTS money_imports(name TEXT PRIMARY KEY);`

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
	var applied int
	err = transaction.QueryRowContext(ctx, "SELECT 1 FROM money_imports WHERE name=?", name).Scan(&applied)
	if err == nil {
		return 0, 0, 0, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, err
	}
	stamp := imported.ProviderReadTime.AsTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	stampProto := prototext.Format(imported.ProviderReadTime)
	for _, accountImport := range imported.MoneyAccountImports {
		account := accountImport.MoneyAccount
		identity := account.GetMoneyAccountIdentifier()
		if identity.GetInstitutionIdentifier() == "" || identity.GetAccountIdentifierNamespace() == "" || identity.GetStableAccountIdentifier() == "" {
			return 0, 0, 0, fmt.Errorf("account requires stable institution, namespace and identity")
		}
		reference := stableReference("account", identity.InstitutionIdentifier, identity.AccountIdentifierNamespace, identity.StableAccountIdentifier)
		account.CanonicalRecordReference = trawlkit.NewCanonicalArchiveRecordReference(reference)
		previous := &contract.MoneyAccount{}
		var previousEncoded []byte
		var previousStamp string
		err := transaction.QueryRowContext(ctx, "SELECT record,read_time FROM money_accounts WHERE reference=?", reference).Scan(&previousEncoded, &previousStamp)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, 0, 0, err
		}
		if err == nil {
			if err := proto.Unmarshal(previousEncoded, previous); err != nil {
				return 0, 0, 0, err
			}
		}
		oldPendingRecords := map[string][]byte{}
		if accountImport.CompletePendingMovementList != nil {
			rows, err := transaction.QueryContext(ctx, "SELECT reference,record FROM money_movements WHERE account_reference=? AND pending=1", reference)
			if err != nil {
				return 0, 0, 0, err
			}
			for rows.Next() {
				var reference string
				var encoded []byte
				if err := rows.Scan(&reference, &encoded); err != nil {
					_ = rows.Close()
					return 0, 0, 0, err
				}
				oldPendingRecords[reference] = encoded
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return 0, 0, 0, err
			}
			_ = rows.Close()
		}
		pendingIsNewer := previous.LastPendingMovementsReadTime == nil || !imported.ProviderReadTime.AsTime().Before(previous.LastPendingMovementsReadTime.AsTime())
		if previousStamp <= stamp {
			replacePopulatedFields(previous, account)
		}
		if accountImport.BookedMovementList != nil && (previous.LastBookedMovementsReadTime == nil || !imported.ProviderReadTime.AsTime().Before(previous.LastBookedMovementsReadTime.AsTime())) {
			previous.LastBookedMovementsReadTime = imported.ProviderReadTime
		}
		if accountImport.CompletePendingMovementList != nil && pendingIsNewer {
			previous.LastPendingMovementsReadTime = imported.ProviderReadTime
			if _, err := transaction.ExecContext(ctx, "DELETE FROM money_search WHERE reference IN (SELECT reference FROM money_movements WHERE account_reference=? AND pending=1)", reference); err != nil {
				return 0, 0, 0, err
			}
			if _, err := transaction.ExecContext(ctx, "DELETE FROM money_movements WHERE account_reference=? AND pending=1", reference); err != nil {
				return 0, 0, 0, err
			}
		}
		encoded, err := proto.Marshal(previous)
		if err != nil {
			return 0, 0, 0, err
		}
		accountStamp := stamp
		if previousStamp > accountStamp {
			accountStamp = previousStamp
		}
		if _, err := transaction.ExecContext(ctx, "INSERT INTO money_accounts VALUES(?,?,?) ON CONFLICT(reference) DO UPDATE SET record=excluded.record,read_time=excluded.read_time", reference, encoded, accountStamp); err != nil {
			return 0, 0, 0, err
		}
		var movements []storedMovement
		for _, movement := range accountImport.GetBookedMovementList().GetBookedMovements() {
			movement.CanonicalRecordReference = trawlkit.NewCanonicalArchiveRecordReference(stableReference("movement", reference, movement.GetMoneyMovementIdentifier().GetMovementIdentifierNamespace(), movement.GetMoneyMovementIdentifier().GetAccountScopedMovementIdentifier()))
			movements = append(movements, storedMovement{booked: movement})
		}
		if pendingIsNewer {
			for _, movement := range accountImport.GetCompletePendingMovementList().GetPendingMovements() {
				movement.CanonicalRecordReference = trawlkit.NewCanonicalArchiveRecordReference(stableReference("pending", reference, movement.GetMoneyMovementIdentifier().GetMovementIdentifierNamespace(), movement.GetMoneyMovementIdentifier().GetAccountScopedMovementIdentifier()))
				movements = append(movements, storedMovement{pending: movement})
			}
		}
		for _, movement := range movements {
			var record proto.Message
			var identifier *contract.MoneyMovementIdentifier
			if movement.booked != nil {
				record = movement.booked
				identifier = movement.booked.MoneyMovementIdentifier
			} else {
				record = movement.pending
				identifier = movement.pending.MoneyMovementIdentifier
			}
			if identifier.GetMovementIdentifierNamespace() == "" || identifier.GetAccountScopedMovementIdentifier() == "" {
				return 0, 0, 0, fmt.Errorf("movement requires namespace and account-scoped identifier")
			}
			var old []byte
			var oldStamp string
			err := transaction.QueryRowContext(ctx, "SELECT record,read_time FROM money_movements WHERE reference=?", movement.reference()).Scan(&old, &oldStamp)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return 0, 0, 0, err
			}
			if movement.pending != nil && errors.Is(err, sql.ErrNoRows) {
				if prior := oldPendingRecords[movement.reference()]; prior != nil {
					old = prior
					oldStamp = stampProto
					err = nil
				}
			}
			delete(oldPendingRecords, movement.reference())
			recordProvider := imported.ProviderDisplayName
			recordReadTime := stampProto
			if err == nil {
				oldTime := &timestamppb.Timestamp{}
				if err := prototext.Unmarshal([]byte(oldStamp), oldTime); err != nil {
					return 0, 0, 0, err
				}

				merged := record.ProtoReflect().New().Interface()
				if err := proto.Unmarshal(old, merged); err != nil {
					return 0, 0, 0, err
				}
				if oldTime.AsTime().After(imported.ProviderReadTime.AsTime()) {
					replacePopulatedFields(record, merged)
					recordReadTime = oldStamp
					var provider string
					if err := transaction.QueryRowContext(ctx, "SELECT provider FROM money_movements WHERE reference=?", movement.reference()).Scan(&provider); err != nil {
						return 0, 0, 0, err
					}
					recordProvider = provider
				} else {
					replacePopulatedFields(merged, record)
					record = merged
				}
			}

			type indexedMoneyField struct {
				label string
				text  string
			}
			var fields []indexedMoneyField
			if movement.pending != nil {
				m := record.(*contract.MoneyPendingMovement)
				fields = []indexedMoneyField{{"Transaction", nonemptyJoined(m.CounterpartyDisplayName, m.MovementDescription)}, {"Provider reference", m.GetMoneyMovementIdentifier().GetAccountScopedMovementIdentifier()}}
			} else {
				m := record.(*contract.MoneyMovement)
				fields = []indexedMoneyField{{"Transaction", nonemptyJoined(m.CounterpartyDisplayName, m.MovementDescription)}, {"Reference", m.PaymentReference}, {"Account", m.CounterpartyAccountIdentifier}, {"Mandate", m.DirectDebitMandateIdentifier}, {"Creditor", m.DirectDebitCreditorIdentifier}, {"Card member", m.CardMemberDisplayName}, {"Place", nonemptyJoined(m.MerchantCity, m.MerchantCountryName)}, {"Details", strings.Join(m.AdditionalPaymentDetails, " ")}, {"Provider reference", m.GetMoneyMovementIdentifier().GetAccountScopedMovementIdentifier()}, {"Merchant category", m.MerchantCategoryCode}, {"Bank transaction code", m.ProviderBankTransactionCode}}
				if flight := m.FlightTicket; flight != nil {
					fields = append(fields, indexedMoneyField{"Flight", nonemptyJoined(flight.DeparturePlace, flight.ArrivalPlace, flight.AirlineCode, flight.BookingClassCode, flight.TicketNumber, flight.PassengerName)})
				}
			}
			if _, err := transaction.ExecContext(ctx, "DELETE FROM money_search WHERE reference=?", movement.reference()); err != nil {
				return 0, 0, 0, err
			}
			for _, field := range fields {
				if field.text == "" {
					continue
				}
				if _, err := transaction.ExecContext(ctx, "INSERT INTO money_search(reference,field_label,body) VALUES(?,?,?)", movement.reference(), field.label, field.text); err != nil {
					return 0, 0, 0, err
				}
			}
			encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(record)
			if err != nil {
				return 0, 0, 0, err
			}
			if old == nil {
				added++
			} else {
				oldRecord := record.ProtoReflect().New().Interface()
				if err := proto.Unmarshal(old, oldRecord); err != nil {
					return 0, 0, 0, err
				}
				if !proto.Equal(oldRecord, record) {
					changed++
				}
			}
			if _, err := transaction.ExecContext(ctx, "INSERT INTO money_movements VALUES(?,?,?,?,?,?) ON CONFLICT(reference) DO UPDATE SET record=excluded.record,provider=excluded.provider,read_time=excluded.read_time", movement.reference(), reference, movement.pending != nil, encoded, recordProvider, recordReadTime); err != nil {
				return 0, 0, 0, err
			}
		}
		if accountImport.CompletePendingMovementList != nil && pendingIsNewer {
			removed += uint64(len(oldPendingRecords))
		}
	}
	rows, err := transaction.QueryContext(ctx, "SELECT reference FROM money_accounts UNION SELECT reference FROM money_movements")
	if err != nil {
		return 0, 0, 0, err
	}
	var references []trawlkit.ShortReferenceAssignmentCandidate
	for rows.Next() {
		var reference string
		if err := rows.Scan(&reference); err != nil {
			_ = rows.Close()
			return 0, 0, 0, err
		}
		canonical := trawlkit.NewCanonicalArchiveRecordReference(reference)
		references = append(references, trawlkit.ShortReferenceAssignmentCandidate{StableRecordReferenceUsedForShortReferenceAssignment: canonical, CurrentRecordReferenceReturnedWhenShortReferenceIsResolved: canonical})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, 0, err
	}
	_ = rows.Close()
	if err := trawlkit.AssignShortReferencesForObservedArchiveRecordsUsingCallerOwnedSQLTransaction(ctx, transaction, references); err != nil {
		return 0, 0, 0, err
	}
	if _, err := transaction.ExecContext(ctx, "INSERT INTO money_imports VALUES(?)", name); err != nil {
		return 0, 0, 0, err
	}
	return added, changed, removed, transaction.Commit()
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
