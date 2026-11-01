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
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	contract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/money"
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
func enableBankingToken(settings *enableBankingSettings) (string, error) {
	encoded, err := os.ReadFile(settings.PrivateKeyFile)
	if err != nil {
		return "", err
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

func collectEnableBankingConnection(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, settings *enableBankingSettings, connection enableBankingConnectionSettings) error {
	encoded, err := os.ReadFile(connection.SessionFile)
	if err != nil {
		return err
	}
	credential := struct {
		SessionID string `json:"session_id"`
	}{}
	if err := json.Unmarshal(encoded, &credential); err != nil {
		return err
	}
	if credential.SessionID == "" {
		return fmt.Errorf("enable banking session has no session_id")
	}
	session := enableBankingSession{SessionID: credential.SessionID}
	token, err := enableBankingToken(settings)
	if err != nil {
		return err
	}
	attemptedTime := time.Now().UTC()
	refreshClaim, err := req.OpenedTrawlerArchiveStore.DB().ExecContext(ctx, `INSERT INTO money_enable_banking_refresh VALUES(?,?) ON CONFLICT(connection_identifier) DO UPDATE SET attempted_time=excluded.attempted_time WHERE attempted_time<=?`, connection.ConnectionIdentifier, attemptedTime.Format(time.RFC3339Nano), attemptedTime.Add(-24*time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	claimed, err := refreshClaim.RowsAffected()
	if err != nil {
		return err
	}
	if claimed == 0 {
		if req.TrawlerCommandLog != nil {
			_ = req.TrawlerCommandLog.Info("enable_banking_refresh_skipped", string(connection.ConnectionIdentifier)+": a read was attempted within the last 24 hours.")
		}
		return nil
	}
	importsDirectory := filepath.Join(filepath.Dir(req.TrawlerArchivePaths.TrawlerArchivePath), "imports")
	if err := os.MkdirAll(importsDirectory, 0700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(importsDirectory, ".enable-banking-")
	if err != nil {
		return err
	}
	originals := filepath.Join(temporary, "originals")
	if err := os.Mkdir(originals, 0700); err != nil {
		return err
	}
	client := &http.Client{Timeout: 90 * time.Second}
	sequence := 0
	get := func(endpoint string, destination interface{}) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.enablebanking.com"+endpoint, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		sequence++
		if err := os.WriteFile(filepath.Join(originals, fmt.Sprintf("%04d.json", sequence)), body, 0600); err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("enable banking read %d returned HTTP %d", sequence, response.StatusCode)
		}
		return json.Unmarshal(body, destination)
	}
	if err := get("/sessions/"+url.PathEscape(session.SessionID), &session); err != nil {
		return err
	}
	accounts, err := readAccounts(ctx, req)
	if err != nil {
		return err
	}
	collected := &contract.MoneyImport{ProviderDisplayName: session.ASPSP.Name}
	for _, uid := range session.Accounts {
		remote := enableBankingAccount{UID: uid}
		if err := get("/accounts/"+url.PathEscape(uid)+"/details", &remote); err != nil {
			return err
		}
		remote.UID = uid
		var account *contract.MoneyAccount
		for _, candidate := range accounts {
			identity := candidate.GetMoneyAccountIdentifier()
			bound := false
			for _, binding := range connection.Accounts {
				if binding.IdentificationHash != "" && binding.IdentificationHash == remote.IdentificationHash && binding.InstitutionIdentifier == identity.GetInstitutionIdentifier() && binding.AccountIdentifierNamespace == identity.GetAccountIdentifierNamespace() && binding.StableAccountIdentifier == identity.GetStableAccountIdentifier() {
					bound = true
					break
				}
			}
			if bound || (identity.GetAccountIdentifierNamespace() == "iban" && remote.AccountID.IBAN != "" && strings.ReplaceAll(remote.AccountID.IBAN, " ", "") == identity.GetStableAccountIdentifier()) {
				account = candidate
				break
			}
		}
		if account == nil {
			return fmt.Errorf("enable banking account is not bound to an imported stable account identity")
		}
		first := connection.FirstRefreshDate
		if first == "" {
			return fmt.Errorf("enable banking requires first_refresh_date")
		}
		if account.LastBookedMovementsReadTime != nil {
			first = account.LastBookedMovementsReadTime.AsTime().AddDate(0, 0, -7).Format("2006-01-02")
		}
		imported := &contract.MoneyAccountImport{MoneyAccount: account, BookedMovementList: &contract.MoneyBookedMovementList{}}
		query := url.Values{"date_from": {first}, "date_to": {time.Now().Format("2006-01-02")}, "transaction_status": {"BOOK"}}
		for {
			page := enableBankingTransactions{}
			if err := get("/accounts/"+url.PathEscape(remote.UID)+"/transactions?"+query.Encode(), &page); err != nil {
				return err
			}
			for _, row := range page.Transactions {
				if row.EntryReference == "" {
					return fmt.Errorf("enable banking returned a transaction without an entry reference")
				}
				amount := row.TransactionAmount
				party := row.Debtor.Name
				if row.CreditDebitIndicator == "DBIT" {
					amount.Amount = "-" + strings.TrimPrefix(amount.Amount, "-")
					party = row.Creditor.Name
				} else if row.CreditDebitIndicator != "CRDT" {
					return fmt.Errorf("enable banking returned unknown credit/debit direction")
				}
				movement := &contract.MoneyMovement{MoneyMovementIdentifier: &contract.MoneyMovementIdentifier{MovementIdentifierNamespace: "enable-banking-entry-reference", AccountScopedMovementIdentifier: row.EntryReference}, SignedAmountIntoMoneyAccount: bankingAmount(amount), BookingDate: bankingDate(row.BookingDate), ValueDate: bankingDate(row.ValueDate), CounterpartyDisplayName: party, MerchantCategoryCode: row.MerchantCategoryCode}
				switch session.ASPSP.Name {
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
					return fmt.Errorf("enable banking institution is not supported")
				}
				imported.BookedMovementList.BookedMovements = append(imported.BookedMovementList.BookedMovements, movement)
			}
			if page.ContinuationKey == "" {
				break
			}
			query.Set("continuation_key", page.ContinuationKey)
		}
		if session.ASPSP.Name == "Wise" {
			imported.CompletePendingMovementList = &contract.MoneyPendingMovementList{}
			pendingQuery := url.Values{"transaction_status": {"PDNG"}}
			for {
				page := enableBankingTransactions{}
				if err := get("/accounts/"+url.PathEscape(remote.UID)+"/transactions?"+pendingQuery.Encode(), &page); err != nil {
					return err
				}
				for _, row := range page.Transactions {
					if row.EntryReference == "" {
						return fmt.Errorf("enable banking returned pending activity without an entry reference")
					}
					amount := row.TransactionAmount
					party := row.Debtor.Name
					if row.CreditDebitIndicator == "DBIT" {
						amount.Amount = "-" + strings.TrimPrefix(amount.Amount, "-")
						party = row.Creditor.Name
					} else if row.CreditDebitIndicator != "CRDT" {
						return fmt.Errorf("enable banking returned unknown pending credit/debit direction")
					}
					imported.CompletePendingMovementList.PendingMovements = append(imported.CompletePendingMovementList.PendingMovements, &contract.MoneyPendingMovement{MoneyMovementIdentifier: &contract.MoneyMovementIdentifier{MovementIdentifierNamespace: "wise-entry-reference-direction", AccountScopedMovementIdentifier: row.EntryReference + ":" + row.CreditDebitIndicator}, SignedAmountIntoMoneyAccount: bankingAmount(amount), ChargeDate: bankingDate(row.TransactionDate), CounterpartyDisplayName: party, MovementDescription: strings.Join(row.Remittance, "\n")})
				}
				if page.ContinuationKey == "" {
					break
				}
				pendingQuery.Set("continuation_key", page.ContinuationKey)
			}
		}
		balances := enableBankingBalances{}
		if err := get("/accounts/"+url.PathEscape(remote.UID)+"/balances", &balances); err != nil {
			return err
		}
		account.MoneyAccountBalances = nil
		for _, balance := range balances.Balances {
			basis := contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_UNSPECIFIED
			switch balance.Basis {
			case "XPCD":
				basis = contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_EXPECTED
			case "CLBD", "ITBD":
				basis = contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_BOOKED
			case "CLAV", "ITAV":
				basis = contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_AVAILABLE
			}
			record := &contract.MoneyBalance{AccountBalanceAmount: bankingAmount(balance.Amount), MoneyBalanceBasis: basis, ProviderBalanceTypeCode: balance.Basis, BalanceReadTime: timestamppb.Now()}
			if date := bankingDate(balance.ReferenceDate); date != nil {
				record.BalanceAsOf = &presentation.ArchiveRecordAssociatedTimeForDisplay{ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{CalendarDate: date}}
			}
			account.MoneyAccountBalances = append(account.MoneyAccountBalances, record)
		}
		collected.MoneyAccountImports = append(collected.MoneyAccountImports, imported)
	}
	collected.ProviderReadTime = timestamppb.Now()
	encoded, err = prototext.Marshal(collected)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(temporary, "import.txtpb"), encoded, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(importsDirectory, "enable-banking-"+time.Now().UTC().Format("20060102T150405.000000000Z")))
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
