package money

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	contract "github.com/opentrawl/opentrawl/trawlers/money/proto/trawl/money"
	"github.com/opentrawl/opentrawl/trawlkit"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"github.com/pelletier/go-toml/v2"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type enableBankingConnectionIdentifier string

type enableBankingAccountBinding struct {
	IdentificationHash         string `toml:"identification_hash"`
	InstitutionIdentifier      string `toml:"institution_identifier"`
	AccountIdentifierNamespace string `toml:"account_identifier_namespace"`
	StableAccountIdentifier    string `toml:"stable_account_identifier"`
}

type enableBankingConnectionSettings struct {
	ConnectionIdentifier enableBankingConnectionIdentifier `toml:"connection_identifier"`
	SessionFile          string                            `toml:"session_file"`
	FirstRefreshDate     string                            `toml:"first_refresh_date"`
	Accounts             []enableBankingAccountBinding     `toml:"accounts"`
}

type enableBankingSettings struct {
	ApplicationID  string                            `toml:"application_id"`
	PrivateKeyFile string                            `toml:"private_key_file"`
	Connections    []enableBankingConnectionSettings `toml:"connections"`
}
type moneyConfiguration struct {
	EnableBanking *enableBankingSettings `toml:"enable_banking"`
}
type enableBankingSession struct {
	SessionID string   `json:"session_id"`
	Accounts  []string `json:"accounts"`
	ASPSP     struct {
		Name string `json:"name"`
	} `json:"aspsp"`
}
type enableBankingAccount struct {
	UID       string `json:"uid"`
	AccountID struct {
		IBAN string `json:"iban"`
	} `json:"account_id"`
	IdentificationHash string `json:"identification_hash"`
	Name               string `json:"name"`
	Currency           string `json:"currency"`
}
type enableBankingAmount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}
type enableBankingTransaction struct {
	EntryReference       string              `json:"entry_reference"`
	TransactionAmount    enableBankingAmount `json:"transaction_amount"`
	CreditDebitIndicator string              `json:"credit_debit_indicator"`
	Status               string              `json:"status"`
	BookingDate          string              `json:"booking_date"`
	ValueDate            string              `json:"value_date"`
	TransactionDate      string              `json:"transaction_date"`
	Creditor             struct {
		Name string `json:"name"`
	} `json:"creditor"`
	Debtor struct {
		Name string `json:"name"`
	} `json:"debtor"`
	Remittance           []string `json:"remittance_information"`
	MerchantCategoryCode string   `json:"merchant_category_code"`
	ReferenceNumber      string   `json:"reference_number"`
	BankTransactionCode  struct {
		Code string `json:"code"`
	} `json:"bank_transaction_code"`
}
type enableBankingTransactions struct {
	Transactions    []enableBankingTransaction `json:"transactions"`
	ContinuationKey string                     `json:"continuation_key"`
}
type enableBankingBalances struct {
	Balances []struct {
		Amount        enableBankingAmount `json:"balance_amount"`
		Basis         string              `json:"balance_type"`
		ReferenceDate string              `json:"reference_date"`
	} `json:"balances"`
}

func bankingDate(text string) *presentation.CalendarDate {
	parsed, err := time.Parse("2006-01-02", text)
	if err != nil {
		return nil
	}
	return &presentation.CalendarDate{CalendarYear: int32(parsed.Year()), CalendarMonthNumber: int32(parsed.Month()), CalendarDayOfMonth: int32(parsed.Day())}
}
func bankingAmount(amount enableBankingAmount) *contract.ExactMonetaryAmount {
	return &contract.ExactMonetaryAmount{ExactDecimalAmount: amount.Amount, Iso_4217CurrencyCode: amount.Currency}
}
func enableBankingToken(req *trawlkit.TrawlerCommandExecutionRequest, settings *enableBankingSettings) (string, error) {
	var encoded []byte
	if req != nil && len(req.BrokeredSecrets["enable_banking_private_key_pem"]) > 0 {
		encoded = req.BrokeredSecrets["enable_banking_private_key_pem"]
	} else {
		var err error
		encoded, err = os.ReadFile(settings.PrivateKeyFile)
		if err != nil {
			return "", err
		}
	}
	block, _ := pem.Decode(encoded)
	if block == nil {
		return "", fmt.Errorf("enable banking key is not PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("read Enable Banking RSA key: %w", err)
	}
	header, err := json.Marshal(struct {
		Type      string `json:"typ"`
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}{"JWT", "RS256", settings.ApplicationID})
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	claims, err := json.Marshal(struct {
		Issuer   string `json:"iss"`
		Audience string `json:"aud"`
		Issued   int64  `json:"iat"`
		Expires  int64  `json:"exp"`
	}{"enablebanking.com", "api.enablebanking.com", now, now + 3600})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
func collectEnableBanking(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) error {
	configurationFile := string(req.TrawlerArchivePaths.TrawlerConfigurationPath)
	encoded, err := os.ReadFile(configurationFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	configuration := moneyConfiguration{}
	if err := toml.Unmarshal(encoded, &configuration); err != nil {
		return err
	}
	settings := configuration.EnableBanking
	if settings == nil {
		return nil
	}
	if settings.ApplicationID == "" || settings.PrivateKeyFile == "" || len(settings.Connections) == 0 {
		return fmt.Errorf("enable banking requires application_id, private_key_file and connections")
	}
	identifiers := map[enableBankingConnectionIdentifier]bool{}
	for _, connection := range settings.Connections {
		if connection.ConnectionIdentifier == "" || connection.SessionFile == "" || bankingDate(connection.FirstRefreshDate) == nil {
			return fmt.Errorf("enable banking connections require connection_identifier, session_file and first_refresh_date")
		}
		if identifiers[connection.ConnectionIdentifier] {
			return fmt.Errorf("enable banking connection_identifier must be unique")
		}
		identifiers[connection.ConnectionIdentifier] = true
	}
	var failures []error
	for _, connection := range settings.Connections {
		if err := collectEnableBankingConnection(ctx, req, settings, connection); err != nil {
			failures = append(failures, fmt.Errorf("enable banking connection %s: %w", connection.ConnectionIdentifier, err))
		}
	}
	return errors.Join(failures...)
}

type enableBankingClient struct {
	httpClient   *http.Client
	token        string
	originalsDir string
	sequence     int
}

func (c *enableBankingClient) getJSON(ctx context.Context, endpoint string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.enablebanking.com"+endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	c.sequence++
	if err := os.WriteFile(filepath.Join(c.originalsDir, fmt.Sprintf("%04d.json", c.sequence)), body, 0600); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("enable banking read %d returned HTTP %d", c.sequence, response.StatusCode)
	}
	return json.Unmarshal(body, destination)
}

func collectEnableBankingConnection(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, settings *enableBankingSettings, connection enableBankingConnectionSettings) error {
	sessionID, err := readEnableBankingSessionID(connection.SessionFile)
	if err != nil {
		return err
	}
	token, err := enableBankingToken(req, settings)
	if err != nil {
		return err
	}
	claimed, err := claimEnableBankingRefreshSlot(ctx, req, connection.ConnectionIdentifier)
	if err != nil || !claimed {
		return err
	}
	importsDirectory := filepath.Join(filepath.Dir(req.TrawlerArchivePaths.TrawlerArchivePath), "imports")
	temporary, originals, err := prepareEnableBankingImportDir(importsDirectory)
	if err != nil {
		return err
	}
	httpClient := req.PluginHTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	api := &enableBankingClient{
		httpClient:   httpClient,
		token:        token,
		originalsDir: originals,
	}
	session := enableBankingSession{SessionID: sessionID}
	if err := api.getJSON(ctx, "/sessions/"+url.PathEscape(session.SessionID), &session); err != nil {
		return err
	}
	accounts, err := readAccounts(ctx, req)
	if err != nil {
		return err
	}
	collected := &contract.MoneyImport{ProviderDisplayName: session.ASPSP.Name}
	for _, uid := range session.Accounts {
		importedAccount, err := collectEnableBankingAccount(ctx, api, session.ASPSP.Name, connection, accounts, uid)
		if err != nil {
			return err
		}
		collected.MoneyAccountImports = append(collected.MoneyAccountImports, importedAccount)
	}
	collected.ProviderReadTime = timestamppb.Now()
	encoded, err := prototext.Marshal(collected)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(temporary, "import.txtpb"), encoded, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(importsDirectory, "enable-banking-"+time.Now().UTC().Format("20060102T150405.000000000Z")))
}

func readEnableBankingSessionID(sessionFile string) (string, error) {
	encoded, err := os.ReadFile(sessionFile)
	if err != nil {
		return "", err
	}
	var credential struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(encoded, &credential); err != nil {
		return "", err
	}
	if credential.SessionID == "" {
		return "", fmt.Errorf("enable banking session has no session_id")
	}
	return credential.SessionID, nil
}

func claimEnableBankingRefreshSlot(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, connectionID enableBankingConnectionIdentifier) (bool, error) {
	attemptedTime := time.Now().UTC()
	refreshClaim, err := req.OpenedTrawlerArchiveStore.DB().ExecContext(ctx, `INSERT INTO money_enable_banking_refresh VALUES(?,?) ON CONFLICT(connection_identifier) DO UPDATE SET attempted_time=excluded.attempted_time WHERE attempted_time<=?`, connectionID, attemptedTime.Format(time.RFC3339Nano), attemptedTime.Add(-24*time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	claimed, err := refreshClaim.RowsAffected()
	if err != nil {
		return false, err
	}
	if claimed == 0 {
		if req.TrawlerCommandLog != nil {
			_ = req.TrawlerCommandLog.Info("enable_banking_refresh_skipped", string(connectionID)+": a read was attempted within the last 24 hours.")
		}
		return false, nil
	}
	return true, nil
}

func prepareEnableBankingImportDir(importsDirectory string) (string, string, error) {
	if err := os.MkdirAll(importsDirectory, 0700); err != nil {
		return "", "", err
	}
	temporary, err := os.MkdirTemp(importsDirectory, ".enable-banking-")
	if err != nil {
		return "", "", err
	}
	originals := filepath.Join(temporary, "originals")
	if err := os.Mkdir(originals, 0700); err != nil {
		return "", "", err
	}
	return temporary, originals, nil
}

func collectEnableBankingAccount(
	ctx context.Context,
	api *enableBankingClient,
	aspspName string,
	connection enableBankingConnectionSettings,
	accounts []*contract.MoneyAccount,
	uid string,
) (*contract.MoneyAccountImport, error) {
	remote := enableBankingAccount{UID: uid}
	if err := api.getJSON(ctx, "/accounts/"+url.PathEscape(uid)+"/details", &remote); err != nil {
		return nil, err
	}
	remote.UID = uid
	account := matchBoundMoneyAccount(accounts, connection.Accounts, remote)
	if account == nil {
		return nil, fmt.Errorf("enable banking account is not bound to an imported stable account identity")
	}
	first, err := enableBankingRefreshStartDate(connection.FirstRefreshDate, account)
	if err != nil {
		return nil, err
	}
	last := time.Now().UTC().Format("2006-01-02")
	booked, err := fetchEnableBankingBookedMovements(ctx, api, aspspName, remote.UID, first, last)
	if err != nil {
		return nil, err
	}
	imported := &contract.MoneyAccountImport{
		MoneyAccount:       account,
		BookedMovementList: &contract.MoneyBookedMovementList{BookedMovements: booked},
	}
	switch aspspName {
	case "Wise":
		recordEnableBankingBookedHistoryCoverage(account, first, last)
		account.HistoryAvailabilityNote = "Statements and fully paged Wise API history are included in the complete history intervals, as of the booked read time. Fees are separate movements."
		pending, err := fetchWisePendingMovements(ctx, api, remote.UID)
		if err != nil {
			return nil, err
		}
		imported.CompletePendingMovementList = &contract.MoneyPendingMovementList{PendingMovements: pending}
	case "ING":
		recordEnableBankingBookedHistoryCoverage(account, first, last)
		account.HistoryAvailabilityNote = "Statements and fully paged ING API history are included in the complete history intervals, as of the booked read time."
	}
	balances, err := fetchEnableBankingBalances(ctx, api, remote.UID)
	if err != nil {
		return nil, err
	}
	account.MoneyAccountBalances = balances
	return imported, nil
}

func matchBoundMoneyAccount(accounts []*contract.MoneyAccount, bindings []enableBankingAccountBinding, remote enableBankingAccount) *contract.MoneyAccount {
	remoteIBAN := strings.ReplaceAll(remote.AccountID.IBAN, " ", "")
	for _, candidate := range accounts {
		identity := candidate.GetMoneyAccountIdentifier()
		if accountMatchesExplicitBinding(identity, bindings, remote.IdentificationHash) {
			return candidate
		}
		if identity.GetAccountIdentifierNamespace() == "iban" && remoteIBAN != "" && remoteIBAN == identity.GetStableAccountIdentifier() {
			return candidate
		}
	}
	return nil
}

func accountMatchesExplicitBinding(identity *contract.MoneyAccountIdentifier, bindings []enableBankingAccountBinding, identificationHash string) bool {
	if identificationHash == "" {
		return false
	}
	for _, binding := range bindings {
		if binding.IdentificationHash == identificationHash &&
			binding.InstitutionIdentifier == identity.GetInstitutionIdentifier() &&
			binding.AccountIdentifierNamespace == identity.GetAccountIdentifierNamespace() &&
			binding.StableAccountIdentifier == identity.GetStableAccountIdentifier() {
			return true
		}
	}
	return false
}

func enableBankingRefreshStartDate(firstRefreshDate string, account *contract.MoneyAccount) (string, error) {
	if firstRefreshDate == "" {
		return "", fmt.Errorf("enable banking requires first_refresh_date")
	}
	first := firstRefreshDate
	if account.LastBookedMovementsReadTime != nil {
		first = account.LastBookedMovementsReadTime.AsTime().AddDate(0, 0, -7).Format("2006-01-02")
	}
	if latestCompleteEnd := latestCompleteBookedHistoryEndDate(account); latestCompleteEnd != "" && latestCompleteEnd >= firstRefreshDate {
		if endDate, err := time.Parse("2006-01-02", latestCompleteEnd); err == nil {
			if first > endDate.AddDate(0, 0, 1).Format("2006-01-02") {
				first = latestCompleteEnd
			}
		}
	}
	return first, nil
}

func enableBankingSignedAmountAndParty(row enableBankingTransaction) (enableBankingAmount, string, error) {
	amount := row.TransactionAmount
	switch row.CreditDebitIndicator {
	case "DBIT":
		amount.Amount = "-" + strings.TrimPrefix(amount.Amount, "-")
		return amount, row.Creditor.Name, nil
	case "CRDT":
		return amount, row.Debtor.Name, nil
	default:
		return enableBankingAmount{}, "", fmt.Errorf("enable banking returned unknown credit/debit direction")
	}
}

func fetchEnableBankingBookedMovements(ctx context.Context, api *enableBankingClient, aspspName, accountUID, first, last string) ([]*contract.MoneyMovement, error) {
	query := url.Values{"date_from": {first}, "date_to": {last}, "transaction_status": {"BOOK"}}
	var movements []*contract.MoneyMovement
	for {
		page := enableBankingTransactions{}
		if err := api.getJSON(ctx, "/accounts/"+url.PathEscape(accountUID)+"/transactions?"+query.Encode(), &page); err != nil {
			return nil, err
		}
		for _, row := range page.Transactions {
			movement, err := convertEnableBankingBookedTransaction(aspspName, row)
			if err != nil {
				return nil, err
			}
			movements = append(movements, movement)
		}
		if page.ContinuationKey == "" {
			return movements, nil
		}
		query.Set("continuation_key", page.ContinuationKey)
	}
}

func convertEnableBankingBookedTransaction(aspspName string, row enableBankingTransaction) (*contract.MoneyMovement, error) {
	if row.EntryReference == "" {
		return nil, fmt.Errorf("enable banking returned a transaction without an entry reference")
	}
	amount, party, err := enableBankingSignedAmountAndParty(row)
	if err != nil {
		return nil, err
	}
	movement := &contract.MoneyMovement{
		MoneyMovementIdentifier: &contract.MoneyMovementIdentifier{
			MovementIdentifierNamespace:     "enable-banking-entry-reference",
			AccountScopedMovementIdentifier: row.EntryReference,
		},
		SignedAmountIntoMoneyAccount: bankingAmount(amount),
		BookingDate:                  bankingDate(row.BookingDate),
		ValueDate:                    bankingDate(row.ValueDate),
		CounterpartyDisplayName:      party,
		MerchantCategoryCode:         row.MerchantCategoryCode,
	}
	switch aspspName {
	case "Wise":
		movement.MoneyMovementIdentifier.MovementIdentifierNamespace = "wise-entry-reference-direction"
		movement.MoneyMovementIdentifier.AccountScopedMovementIdentifier += ":" + row.CreditDebitIndicator
		var remittance []string
		for _, line := range row.Remittance {
			if line != row.ReferenceNumber {
				remittance = append(remittance, line)
			}
		}
		movement.MovementDescription = strings.Join(remittance, "\n")
		movement.ProviderBankTransactionCode = row.BankTransactionCode.Code
	case "ING":
		parseINGRemittance(movement, row.Remittance)
	default:
		return nil, fmt.Errorf("enable banking institution is not supported")
	}
	return movement, nil
}

func fetchWisePendingMovements(ctx context.Context, api *enableBankingClient, accountUID string) ([]*contract.MoneyPendingMovement, error) {
	pendingQuery := url.Values{"transaction_status": {"PDNG"}}
	var movements []*contract.MoneyPendingMovement
	for {
		page := enableBankingTransactions{}
		if err := api.getJSON(ctx, "/accounts/"+url.PathEscape(accountUID)+"/transactions?"+pendingQuery.Encode(), &page); err != nil {
			return nil, err
		}
		for _, row := range page.Transactions {
			if row.EntryReference == "" {
				return nil, fmt.Errorf("enable banking returned pending activity without an entry reference")
			}
			amount, party, err := enableBankingSignedAmountAndParty(row)
			if err != nil {
				return nil, fmt.Errorf("enable banking returned unknown pending credit/debit direction")
			}
			movements = append(movements, &contract.MoneyPendingMovement{
				MoneyMovementIdentifier: &contract.MoneyMovementIdentifier{
					MovementIdentifierNamespace:     "wise-entry-reference-direction",
					AccountScopedMovementIdentifier: row.EntryReference + ":" + row.CreditDebitIndicator,
				},
				SignedAmountIntoMoneyAccount: bankingAmount(amount),
				ChargeDate:                   bankingDate(row.TransactionDate),
				CounterpartyDisplayName:      party,
				MovementDescription:          strings.Join(row.Remittance, "\n"),
			})
		}
		if page.ContinuationKey == "" {
			return movements, nil
		}
		pendingQuery.Set("continuation_key", page.ContinuationKey)
	}
}

func fetchEnableBankingBalances(ctx context.Context, api *enableBankingClient, accountUID string) ([]*contract.MoneyBalance, error) {
	balances := enableBankingBalances{}
	if err := api.getJSON(ctx, "/accounts/"+url.PathEscape(accountUID)+"/balances", &balances); err != nil {
		return nil, err
	}
	out := make([]*contract.MoneyBalance, 0, len(balances.Balances))
	for _, balance := range balances.Balances {
		record := &contract.MoneyBalance{
			AccountBalanceAmount:    bankingAmount(balance.Amount),
			MoneyBalanceBasis:       enableBankingBalanceBasis(balance.Basis),
			ProviderBalanceTypeCode: balance.Basis,
			BalanceReadTime:         timestamppb.Now(),
		}
		if date := bankingDate(balance.ReferenceDate); date != nil {
			record.BalanceAsOf = &presentation.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{CalendarDate: date},
			}
		}
		out = append(out, record)
	}
	return out, nil
}

func enableBankingBalanceBasis(code string) contract.MoneyBalanceBasis {
	switch code {
	case "XPCD":
		return contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_EXPECTED
	case "CLBD", "ITBD":
		return contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_BOOKED
	case "CLAV", "ITAV":
		return contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_AVAILABLE
	default:
		return contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_UNSPECIFIED
	}
}

func latestCompleteBookedHistoryEndDate(account *contract.MoneyAccount) string {
	latest := ""
	for _, interval := range account.GetCompleteHistoryDateRanges() {
		if interval.GetMovementDateBasis() != contract.MoneyMovementDateBasis_MONEY_MOVEMENT_DATE_BASIS_BOOKING_DATE {
			continue
		}
		if end := calendarDateText(interval.GetLastCompleteDate()); end > latest {
			latest = end
		}
	}
	return latest
}

func recordEnableBankingBookedHistoryCoverage(account *contract.MoneyAccount, first, last string) {
	ranges := append(account.CompleteHistoryDateRanges, &contract.MoneyCompleteHistoryDateRange{
		FirstCompleteDate: bankingDate(first), LastCompleteDate: bankingDate(last),
		MovementDateBasis: contract.MoneyMovementDateBasis_MONEY_MOVEMENT_DATE_BASIS_BOOKING_DATE,
	})
	sort.Slice(ranges, func(left, right int) bool {
		if ranges[left].MovementDateBasis != ranges[right].MovementDateBasis {
			return ranges[left].MovementDateBasis < ranges[right].MovementDateBasis
		}
		return calendarDateText(ranges[left].FirstCompleteDate) < calendarDateText(ranges[right].FirstCompleteDate)
	})
	merged := []*contract.MoneyCompleteHistoryDateRange{}
	for _, interval := range ranges {
		if len(merged) > 0 {
			previous := merged[len(merged)-1]
			previousEnd, _ := time.Parse("2006-01-02", calendarDateText(previous.LastCompleteDate))
			if previous.MovementDateBasis == interval.MovementDateBasis && calendarDateText(interval.FirstCompleteDate) <= previousEnd.AddDate(0, 0, 1).Format("2006-01-02") {
				if calendarDateText(interval.LastCompleteDate) > calendarDateText(previous.LastCompleteDate) {
					previous.LastCompleteDate = interval.LastCompleteDate
				}
				continue
			}
		}
		merged = append(merged, interval)
	}
	account.CompleteHistoryDateRanges = merged
}

func parseINGRemittance(movement *contract.MoneyMovement, lines []string) {
	fields := map[string]string{}
	key := ""
	for _, line := range lines {
		found := false
		for _, label := range []string{"Naam", "Omschrijving", "IBAN", "Kenmerk", "Datum/Tijd", "Valutadatum", "Machtiging ID", "Incassant ID"} {
			if strings.HasPrefix(line, label+":") {
				key = label
				fields[key] = strings.TrimSpace(strings.TrimPrefix(line, label+":"))
				found = true
				break
			}
		}
		if line == "Doorlopende incasso" || line == "Eenmalige incasso" {
			key = ""
			continue
		}
		if !found && key != "" {
			fields[key] += " " + line
		}
	}
	if movement.CounterpartyDisplayName == "" {
		movement.CounterpartyDisplayName = fields["Naam"]
	}
	movement.MovementDescription = fields["Omschrijving"]
	movement.CounterpartyAccountIdentifier = fields["IBAN"]
	movement.PaymentReference = fields["Kenmerk"]
	movement.DirectDebitMandateIdentifier = fields["Machtiging ID"]
	movement.DirectDebitCreditorIdentifier = fields["Incassant ID"]
	for _, layout := range []string{"02-01-2006 15:04:05", "02-01-2006 15:04"} {
		if clock, err := time.Parse(layout, fields["Datum/Tijd"]); err == nil {
			movement.PaymentTime = &contract.MoneyPaymentLocalTime{PaymentLocalDate: bankingDate(clock.Format("2006-01-02")), PaymentLocalHour: uint32(clock.Hour()), PaymentLocalMinute: uint32(clock.Minute())}
			movement.PaymentMethod = contract.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_ONLINE_BANKING
			break
		}
	}
	if movement.DirectDebitMandateIdentifier != "" {
		movement.PaymentMethod = contract.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_DIRECT_DEBIT
	}
}
