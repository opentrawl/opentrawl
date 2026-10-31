package money

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	contract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/money"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type App struct{}

func New() *App                                                                   { return &App{} }
func (*App) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error { return nil }
func (*App) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{RegisteredTrawler: trawlkit.NewRegisteredTrawlerIdentity("money"), RegisteredTrawlerCommandName: "money", RegisteredTrawlerDisplayName: "Money", RegisteredTrawlerPrivacyBoundary: control.Privacy{Reads: "Completed financial imports.", LeavesMachine: "Nothing when reading the archive.", NetworkRequests: "Configured Enable Banking consent only."}}
}
func (a *App) TrawlerCommands() []trawlkit.TrawlerCommand {
	var commands []trawlkit.TrawlerCommand
	for _, operation := range []federation.SharedTrawlerOperation{federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN} {
		commands = append(commands, trawlkit.TrawlerCommand{SharedTrawlerOperation: operation, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand})
	}
	commands = append(commands, trawlkit.TrawlerCommand{TrawlerCommandName: "accounts", TrawlerCommandHelpDescription: "List balances and complete history", TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp, TrawlerCommandArchiveAccess: trawlkit.TrawlerCommandArchiveAccessRequired, ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
		accounts, err := readAccounts(ctx, req)
		return &command.TrawlerCommandResponse{TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_MoneyAccountListResponse{MoneyAccountListResponse: &contract.MoneyAccountListResponse{MoneyAccountsInDisplayOrder: accounts}}}, err
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
		response := &contract.MoneyMovementListResponse{ListsPendingMovements: pending, NextMaximumMovementCount: uint32(limit + 10)}
		if basis == "charged" {
			response.MovementDateBasis = contract.MoneyMovementDateBasis_MONEY_MOVEMENT_DATE_BASIS_CHARGE_DATE
		} else {
			response.MovementDateBasis = contract.MoneyMovementDateBasis_MONEY_MOVEMENT_DATE_BASIS_BOOKING_DATE
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
		response.TotalMatchingMovementCount = uint64(len(matching))
		response.MoreMatchingMovementsExist = len(matching) > limit
		if len(matching) > limit {
			matching = matching[:limit]
		}
		for _, account := range accounts {
			group := &contract.MoneyAccountMovementList{MoneyAccount: account}
			for _, movement := range matching {
				if movement.accountReference != trawlkit.CanonicalArchiveRecordReferenceText(account.CanonicalRecordReference) {
					continue
				}
				if pending {
					group.PendingMovementsNewestFirst = append(group.PendingMovementsNewestFirst, movement.pending)
				} else {
					group.BookedMovementsNewestFirst = append(group.BookedMovementsNewestFirst, movement.booked)
				}
			}
			if len(group.BookedMovementsNewestFirst)+len(group.PendingMovementsNewestFirst) > 0 {
				response.MoneyAccountMovementLists = append(response.MoneyAccountMovementLists, group)
			}
		}
		return &command.TrawlerCommandResponse{TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_MoneyMovementListResponse{MoneyMovementListResponse: response}}, nil
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
	response.TrawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	response.TrawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{{ArchiveContentKindName: "accounts", ArchiveContentKindDisplayName: "accounts", ArchiveContentCount: uint64(len(accounts))}}
	return response, nil
}
func (*App) OpenRecord(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, local *trawlkit.LocalTrawlerShortReference) (*open.OpenRecord, error) {
	refs, err := req.ResolveShortReference(ctx, local)
	if err != nil {
		return nil, err
	}
	reference := trawlkit.CanonicalArchiveRecordReferenceText(refs[0])
	accounts, err := readAccounts(ctx, req)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		assigned, err := req.LocalShortReferencesForCanonicalArchiveRecordReferences(ctx, []*trawlkit.CanonicalArchiveRecordReference{account.CanonicalRecordReference})
		if err != nil {
			return nil, err
		}
		local := trawlkit.LocalTrawlerShortReferenceForCanonicalArchiveRecordReference(assigned, account.CanonicalRecordReference)
		account.GloballyRoutableAccountLink, err = trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{RegisteredTrawler: trawlkit.NewRegisteredTrawlerIdentity("money"), LocalShortReference: local})
		if err != nil {
			return nil, err
		}
	}
	record := &open.OpenRecord{RecordTrawler: trawlkit.NewRegisteredTrawlerIdentity("money"), CanonicalRecordReference: refs[0]}
	for _, account := range accounts {
		if trawlkit.CanonicalArchiveRecordReferenceText(account.CanonicalRecordReference) == reference {
			record.TypedOpenedRecord = &open.OpenRecord_MoneyAccount{MoneyAccount: account}
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
					record.TypedOpenedRecord = &open.OpenRecord_OpenedMoneyPendingMovement{OpenedMoneyPendingMovement: &contract.OpenedMoneyPendingMovement{MoneyPendingMovement: movement.pending, MoneyAccount: account, ProviderDisplayName: movement.provider, ProviderReadTime: movement.readTime}}
				} else {
					record.TypedOpenedRecord = &open.OpenRecord_OpenedMoneyMovement{OpenedMoneyMovement: &contract.OpenedMoneyMovement{MoneyMovement: movement.booked, MoneyAccount: account, ProviderDisplayName: movement.provider, ProviderReadTime: movement.readTime}}
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
