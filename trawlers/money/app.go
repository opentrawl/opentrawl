package money

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type App struct{}

var (
	_ trawlkit.CanonicalRecordOpener      = (*App)(nil)
	_ trawlkit.ArchiveIndexChangeExporter = (*App)(nil)
)

func New() *App                                                                   { return &App{} }
func (*App) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error { return nil }
func (*App) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity("money"),
		RegisteredTrawlerCommandName: "money",
		RegisteredTrawlerDisplayName: "Money",
		RegisteredTrawlerBranding: &federation.TrawlerBranding{
			SymbolName:                   "banknote.fill",
			AccentColor:                  "#0A9A63",
			IconNameInMacAppAssetCatalog: "MoneyTrawlerIcon",
		},
		SourceFreshnessDependency: federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_NONE,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Completed financial imports.",
			LeavesMachine:   "Nothing when reading the archive.",
			NetworkRequests: "Configured Enable Banking consent only.",
		},
	}
}

type moneyTransactionsCommandOptions struct {
	account      string
	after        string
	before       string
	basis        string
	matchingText string
	pending      bool
	limit        int
}

func (a *App) TrawlerCommands() []trawlkit.TrawlerCommand {
	var commands []trawlkit.TrawlerCommand
	for _, operation := range []federation.SharedTrawlerOperation{
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
	} {
		commands = append(commands, trawlkit.TrawlerCommand{SharedTrawlerOperation: operation, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand})
	}
	commands = append(commands, trawlkit.TrawlerCommand{
		TrawlerCommandName:               "accounts",
		TrawlerCommandHelpDescription:    "List balances and complete history",
		TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessRequired,
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
			accounts, err := readAccounts(ctx, req)
			if err != nil {
				return nil, err
			}
			return buildMoneyAccountsPresentation(accounts), nil
		},
	})
	var opts moneyTransactionsCommandOptions
	commands = append(commands, trawlkit.TrawlerCommand{
		TrawlerCommandName:               "transactions",
		TrawlerCommandHelpDescription:    "List financial transactions",
		TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessRequired,
		RegisterTrawlerCommandFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.account, "account", "", "Account link")
			fs.StringVar(&opts.after, "after", "", "First date, YYYY-MM-DD")
			fs.StringVar(&opts.before, "before", "", "Last date, YYYY-MM-DD")
			fs.StringVar(&opts.basis, "date-basis", "booked", "booked or charged")
			fs.BoolVar(&opts.pending, "pending", false, "List pending activity")
			fs.IntVar(&opts.limit, "limit", 50, "Maximum transactions")
			fs.StringVar(&opts.matchingText, "matching", "", "Words to find in transaction details")
		},
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
			return runMoneyTransactionsCommand(ctx, req, opts)
		},
	})
	return commands
}

func runMoneyTransactionsCommand(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	opts moneyTransactionsCommandOptions,
) (*command.TrawlerCommandResponse, error) {
	if opts.limit < 1 {
		return nil, fmt.Errorf("limit must be positive")
	}
	if opts.basis != "booked" && opts.basis != "charged" {
		return nil, fmt.Errorf("date basis must be booked or charged")
	}
	selectedAccountRef, err := resolveOptionalMoneyAccountLink(ctx, req, opts.account)
	if err != nil {
		return nil, err
	}
	accounts, err := readAccounts(ctx, req)
	if err != nil {
		return nil, err
	}
	movements, err := readMovements(ctx, req, opts.pending)
	if err != nil {
		return nil, err
	}
	var matchingFields map[string]moneyMatchingField
	if strings.TrimSpace(opts.matchingText) != "" {
		matchingFields, err = matchingMoneyMovementFields(ctx, req, opts.matchingText)
		if err != nil {
			return nil, err
		}
	}
	matching := filterMoneyMovements(movements, selectedAccountRef, matchingFields, opts)
	sort.SliceStable(matching, func(i, j int) bool { return matching[i].date(opts.basis) > matching[j].date(opts.basis) })
	totalMatching := len(matching)
	if len(matching) > opts.limit {
		matching = matching[:opts.limit]
	}
	return buildMoneyTransactionsPresentation(accounts, matching, totalMatching, opts.limit, opts.basis, opts.pending), nil
}

func resolveOptionalMoneyAccountLink(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, account string) (string, error) {
	if account == "" {
		return "", nil
	}
	local, ok, err := trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(account, "money")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("use an account link from trawl money accounts")
	}
	refs, err := req.ResolveShortReference(ctx, trawlkit.NewLocalTrawlerShortReference(local))
	if err != nil {
		return "", err
	}
	return trawlkit.CanonicalArchiveRecordReferenceText(refs[0]), nil
}

func filterMoneyMovements(
	movements []storedMovement,
	selectedAccountRef string,
	matchingFields map[string]moneyMatchingField,
	opts moneyTransactionsCommandOptions,
) []storedMovement {
	var matching []storedMovement
	for _, movement := range movements {
		if !movementMatchesTransactionOptions(movement, selectedAccountRef, matchingFields, opts) {
			continue
		}
		matching = append(matching, movement)
	}
	return matching
}

func movementMatchesTransactionOptions(
	movement storedMovement,
	selectedAccountRef string,
	matchingFields map[string]moneyMatchingField,
	opts moneyTransactionsCommandOptions,
) bool {
	if matchingFields != nil {
		if _, matched := matchingFields[movement.reference()]; !matched {
			return false
		}
	}
	if selectedAccountRef != "" && selectedAccountRef != movement.accountReference {
		return false
	}
	if !opts.pending && opts.basis == "charged" && movement.booked.ChargeDate == nil {
		return false
	}
	if !opts.pending && opts.basis == "booked" && (opts.after != "" || opts.before != "") && movement.booked.BookingDate == nil {
		return false
	}
	date := movement.date(opts.basis)
	if opts.after != "" && date < opts.after {
		return false
	}
	if opts.before != "" && date > opts.before {
		return false
	}
	return true
}
func (*App) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: &status.TrawlerArchiveStatus{}}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	accounts, err := readAccounts(ctx, req)
	if err != nil {
		return response, nil
	}
	var stamp string
	err = req.OpenedTrawlerArchiveStore.DB().QueryRowContext(ctx, "SELECT completed_time FROM money_update_completion WHERE singleton=1").Scan(&stamp)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if stamp != "" {
		completed := &timestamppb.Timestamp{}
		if err := prototext.Unmarshal([]byte(stamp), completed); err != nil {
			return nil, err
		}
		response.TrawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = completed
	}
	if newestDate, err := newestMoneyMovementDate(ctx, req); err == nil && newestDate != "" {
		if parsed, err := time.ParseInLocation("2006-01-02", newestDate, time.Local); err == nil {
			response.TrawlerArchiveStatus.NewestArchivedRecordTime = timestamppb.New(parsed)
		}
	}
	response.TrawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	response.TrawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{{ArchiveContentKindName: "accounts", ArchiveContentKindDisplayName: "accounts", ArchiveContentCount: uint64(len(accounts))}}
	return response, nil
}

func newestMoneyMovementDate(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (string, error) {
	newest := ""
	for _, pending := range []bool{false, true} {
		movements, err := readMovements(ctx, req, pending)
		if err != nil {
			return "", err
		}
		for _, movement := range movements {
			if d := movement.date("booked"); d > newest {
				newest = d
			}
		}
	}
	return newest, nil
}
func (*App) OpenCanonicalRecord(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, canonicalReference *trawlkit.CanonicalArchiveRecordReference) (*open.OpenRecord, error) {
	reference := trawlkit.CanonicalArchiveRecordReferenceText(canonicalReference)
	accounts, err := readAccounts(ctx, req)
	if err != nil {
		return nil, err
	}
	record := &open.OpenRecord{RecordTrawler: trawlkit.NewRegisteredTrawlerIdentity("money"), CanonicalRecordReference: canonicalReference}
	for _, account := range accounts {
		if trawlkit.CanonicalArchiveRecordReferenceText(account.CanonicalRecordReference) == reference {
			record.TypedOpenedRecord = &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
				TrawlerSpecificOpenedRecordPresentation: buildOpenedMoneyAccountPresentation(account),
			}
			return record, nil
		}
	}
	for _, pending := range []bool{false, true} {
		movements, err := readMovements(ctx, req, pending)
		if err != nil {
			return nil, err
		}
		for _, movement := range movements {
			if movement.reference() != reference {
				continue
			}
			for _, account := range accounts {
				if trawlkit.CanonicalArchiveRecordReferenceText(account.CanonicalRecordReference) != movement.accountReference {
					continue
				}
				if pending {
					record.TypedOpenedRecord = &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
						TrawlerSpecificOpenedRecordPresentation: buildOpenedMoneyPendingMovementPresentation(movement.pending, account, movement.provider, movement.readTime),
					}
				} else {
					record.TypedOpenedRecord = &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
						TrawlerSpecificOpenedRecordPresentation: buildOpenedMoneyMovementPresentation(movement.booked, account, movement.provider, movement.readTime),
					}
				}
				return record, nil
			}
		}
	}
	return nil, fmt.Errorf("money record not found")
}
func calendarDateText(date *presentation.CalendarDate) string {
	if date == nil {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", date.CalendarYear, date.CalendarMonthNumber, date.CalendarDayOfMonth)
}
func nonemptyJoined(parts ...string) string {
	var text []string
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			text = append(text, part)
		}
	}
	return strings.Join(text, " · ")
}
