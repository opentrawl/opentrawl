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
	_ trawlkit.CanonicalRecordOpener          = (*App)(nil)
	_ trawlkit.ShortReferenceSnapshotProvider = (*App)(nil)
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
func (a *App) TrawlerCommands() []trawlkit.TrawlerCommand {
	var commands []trawlkit.TrawlerCommand
	for _, operation := range []federation.SharedTrawlerOperation{federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN} {
		commands = append(commands, trawlkit.TrawlerCommand{SharedTrawlerOperation: operation, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand})
	}
	commands = append(commands, trawlkit.TrawlerCommand{TrawlerCommandName: "accounts", TrawlerCommandHelpDescription: "List balances and complete history", TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp, TrawlerCommandArchiveAccess: trawlkit.TrawlerCommandArchiveAccessRequired, ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
		accounts, err := readAccounts(ctx, req)
		if err != nil {
			return nil, err
		}
		return buildMoneyAccountsPresentation(accounts), nil
	}})
	var account, after, before, basis, matchingText string
	var pending bool
	var limit int
	commands = append(commands, trawlkit.TrawlerCommand{TrawlerCommandName: "transactions", TrawlerCommandHelpDescription: "List financial transactions", TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp, TrawlerCommandArchiveAccess: trawlkit.TrawlerCommandArchiveAccessRequired, RegisterTrawlerCommandFlags: func(fs *flag.FlagSet) {
		fs.StringVar(&account, "account", "", "Account link")
		fs.StringVar(&after, "after", "", "First date, YYYY-MM-DD")
		fs.StringVar(&before, "before", "", "Last date, YYYY-MM-DD")
		fs.StringVar(&basis, "date-basis", "booked", "booked or charged")
		fs.BoolVar(&pending, "pending", false, "List pending activity")
		fs.IntVar(&limit, "limit", 50, "Maximum transactions")
		fs.StringVar(&matchingText, "matching", "", "Words to find in transaction details")
	}, ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
		if limit < 1 {
			return nil, fmt.Errorf("limit must be positive")
		}
		if basis != "booked" && basis != "charged" {
			return nil, fmt.Errorf("date basis must be booked or charged")
		}
		selected := ""
		if account != "" {
			local, ok, err := trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(account, "money")
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("use an account link from trawl money accounts")
			}
			refs, err := req.ResolveShortReference(ctx, trawlkit.NewLocalTrawlerShortReference(local))
			if err != nil {
				return nil, err
			}
			selected = trawlkit.CanonicalArchiveRecordReferenceText(refs[0])
		}
		accounts, err := readAccounts(ctx, req)
		if err != nil {
			return nil, err
		}
		movements, err := readMovements(ctx, req, pending)
		if err != nil {
			return nil, err
		}
		var matchingFields map[string]moneyMatchingField
		if strings.TrimSpace(matchingText) != "" {
			matchingFields, err = matchingMoneyMovementFields(ctx, req, matchingText)
			if err != nil {
				return nil, err
			}
		}
		var matching []storedMovement
		for _, movement := range movements {
			if matchingFields != nil {
				if _, matched := matchingFields[movement.reference()]; !matched {
					continue
				}
			}
			if selected != "" && selected != movement.accountReference {
				continue
			}
			if !pending && basis == "charged" && movement.booked.ChargeDate == nil {
				continue
			}
			if !pending && basis == "booked" && (after != "" || before != "") && movement.booked.BookingDate == nil {
				continue
			}
			date := movement.date(basis)
			if after != "" && date < after {
				continue
			}
			if before != "" && date > before {
				continue
			}
			matching = append(matching, movement)
		}
		sort.SliceStable(matching, func(i, j int) bool { return matching[i].date(basis) > matching[j].date(basis) })
		totalMatching := len(matching)
		if len(matching) > limit {
			matching = matching[:limit]
		}
		return buildMoneyTransactionsPresentation(accounts, matching, totalMatching, limit, basis, pending), nil
	}})
	return commands
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
