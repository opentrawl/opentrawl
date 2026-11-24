package pluginruntime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/pluginproxy"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
	worker "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker"
	"github.com/opentrawl/opentrawl/trawlkit/prototransport"
)

const defaultPluginHTTPClientTimeout = 90 * time.Second

// ServePluginExecutable runs a standalone Go trawler executable speaking the
// raw length-delimited binary protobuf plugin protocol over stdin and stdout.
func ServePluginExecutable(trawler trawlkit.Trawler) int {
	stdout, _ := output.ProcessOutputStreams()
	return ServePluginStreams(context.Background(), trawler, os.Stdin, stdout)
}

// ServeBundledPluginWorkerError writes a terminal plugin error frame to stdout
// when multi-call worker dispatch receives invalid or unknown plugin arguments.
func ServeBundledPluginWorkerError(err error) int {
	stdout, _ := output.ProcessOutputStreams()
	writer := &serializedPluginFrameWriter{w: stdout}
	return writeTerminalPluginError(writer, err)
}

// ServePluginStreams runs one plugin invocation against explicit stdin/stdout
// streams and returns the canonical OpenTrawl process exit code.
func ServePluginStreams(
	ctx context.Context,
	trawler trawlkit.Trawler,
	stdin io.Reader,
	stdout io.Writer,
) int {
	writer := &serializedPluginFrameWriter{w: stdout}
	stopParentWatch, watchErr := trawlkit.WatchExternalPluginParentLifetime()
	if watchErr != nil {
		return writeTerminalPluginError(writer, watchErr)
	}
	defer stopParentWatch()

	var request plugincontract.PluginInvocationRequest
	if err := prototransport.ReadDelimited(bufio.NewReader(stdin), &request); err != nil {
		return writeTerminalPluginError(writer, fmt.Errorf("read plugin invocation request: %w", err))
	}

	if request.GetIsInternalAppRequest() {
		ctx = trawlkit.WithInternalAppRequest(ctx)
	}

	result, err := dispatchPluginInvocationRequest(ctx, trawler, &request, writer)
	if err != nil {
		return writeTerminalPluginError(writer, err)
	}
	if err := writer.WriteFrame(&plugincontract.PluginStreamFrame{
		Kind: &plugincontract.PluginStreamFrame_Result{Result: result},
	}); err != nil {
		return 1
	}
	return 0
}

type serializedPluginFrameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (writer *serializedPluginFrameWriter) WriteFrame(frame *plugincontract.PluginStreamFrame) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return prototransport.WriteDelimited(writer.w, frame)
}

func writeTerminalPluginError(writer *serializedPluginFrameWriter, err error) int {
	description := trawlkit.TrawlerOperationErrorDescription(err)
	_ = writer.WriteFrame(&plugincontract.PluginStreamFrame{
		Kind: &plugincontract.PluginStreamFrame_Result{
			Result: &plugincontract.PluginInvocationResult{
				Error: &worker.Error{
					Code:                description.Code,
					Message:             description.Message,
					LockPath:            description.LockPath,
					PhotosStepsNotBuilt: description.PhotosStepsNotBuilt,
				},
			},
		},
	})
	return trawlkit.ExternalPluginExitCodeForError(err)
}

func dispatchPluginInvocationRequest(
	ctx context.Context,
	trawler trawlkit.Trawler,
	request *plugincontract.PluginInvocationRequest,
	writer *serializedPluginFrameWriter,
) (*plugincontract.PluginInvocationResult, error) {
	if _, isManifest := request.GetOperation().(*plugincontract.PluginInvocationRequest_Manifest); isManifest {
		manifestResp, err := trawlkit.BuildTrawlerPluginManifest(trawler)
		if err != nil {
			return nil, err
		}
		return &plugincontract.PluginInvocationResult{
			Success: &plugincontract.PluginInvocationResult_Manifest{Manifest: manifestResp},
		}, nil
	}

	execReq, err := buildPluginSDKExecutionRequest(request, writer)
	if err != nil {
		return nil, err
	}
	if err := trawler.LoadTrawlerConfiguration(execReq.TrawlerArchivePaths.TrawlerConfigurationPath); err != nil {
		return nil, err
	}

	switch op := request.GetOperation().(type) {
	case *plugincontract.PluginInvocationRequest_Status:
		return runSDKStatus(ctx, trawler, execReq)
	case *plugincontract.PluginInvocationRequest_Update:
		return runSDKUpdate(ctx, trawler, execReq, op.Update, writer)
	case *plugincontract.PluginInvocationRequest_Search:
		return runSDKSearch(ctx, trawler, execReq, op.Search)
	case *plugincontract.PluginInvocationRequest_OpenRecord:
		return runSDKOpenRecord(ctx, trawler, execReq, op.OpenRecord)
	case *plugincontract.PluginInvocationRequest_ExportArchiveIndexChanges:
		return runSDKExportArchiveIndexChanges(ctx, trawler, execReq, op.ExportArchiveIndexChanges, writer)
	case *plugincontract.PluginInvocationRequest_BespokeCommand:
		return runSDKBespokeCommand(ctx, trawler, execReq, op.BespokeCommand, writer)
	case *plugincontract.PluginInvocationRequest_Who:
		return runSDKWho(ctx, trawler, execReq, op.Who)
	case *plugincontract.PluginInvocationRequest_CountPersonMessages:
		return runSDKCountPersonMessages(ctx, trawler, execReq, op.CountPersonMessages)
	case *plugincontract.PluginInvocationRequest_Conversations:
		return runSDKConversations(ctx, trawler, execReq, op.Conversations)
	case *plugincontract.PluginInvocationRequest_Messages:
		return runSDKMessages(ctx, trawler, execReq, op.Messages)
	case *plugincontract.PluginInvocationRequest_PeopleSnapshot:
		return runSDKPeopleSnapshot(ctx, trawler, execReq)
	default:
		return nil, errors.New("plugin invocation request operation is unspecified")
	}
}

func buildPluginSDKExecutionRequest(
	request *plugincontract.PluginInvocationRequest,
	writer *serializedPluginFrameWriter,
) (*trawlkit.TrawlerCommandExecutionRequest, error) {
	execReq := &trawlkit.TrawlerCommandExecutionRequest{
		TrawlerArchivePaths: trawlkit.TrawlerArchivePaths{
			TrawlerArchiveDirectoryPath: request.GetArchiveDirectoryPath(),
			TrawlerArchivePath:          request.GetArchiveDatabasePath(),
			TrawlerConfigurationPath:    trawlkit.TrawlerConfigurationFilePath(request.GetConfigurationFilePath()),
		},
		ReportTrawlerCommandProgress: func(progress trawlkit.Progress) {
			_ = writer.WriteFrame(&plugincontract.PluginStreamFrame{
				Kind: &plugincontract.PluginStreamFrame_Progress{
					Progress: &worker.Progress{
						Phase:   progress.Phase,
						Done:    progress.Done,
						Total:   progress.Total,
						Message: progress.Message,
					},
				},
			})
		},
		RecordSecretMutation: func(secretKey string, secretBytes []byte, deleteSecret bool) error {
			return writer.WriteFrame(&plugincontract.PluginStreamFrame{
				Kind: &plugincontract.PluginStreamFrame_SecretMutation{
					SecretMutation: &plugincontract.PluginSecretMutation{
						SecretKey:    secretKey,
						SecretBytes:  append([]byte(nil), secretBytes...),
						DeleteSecret: deleteSecret,
					},
				},
			})
		},
		RequestOpenBrowserURL: func(targetURL string) error {
			return writer.WriteFrame(&plugincontract.PluginStreamFrame{
				Kind: &plugincontract.PluginStreamFrame_OpenBrowserUrl{
					OpenBrowserUrl: &plugincontract.PluginOpenBrowserURLNotice{Url: targetURL},
				},
			})
		},
	}

	if mappings := request.GetPreResolvedLocalShortReferences(); len(mappings) > 0 {
		byAlias := make(map[string]string, len(mappings))
		for _, mapping := range mappings {
			alias := trawlkit.LocalTrawlerShortReferenceText(mapping.GetLocalShortReference())
			canonical := trawlkit.CanonicalArchiveRecordReferenceText(mapping.GetCanonicalRecordReference())
			if alias != "" && canonical != "" {
				byAlias[alias] = canonical
			}
		}
		execReq.SetPreResolvedLocalShortReferences(byAlias)
	}

	if secretEntries := request.GetBrokeredSecrets(); len(secretEntries) > 0 {
		execReq.BrokeredSecrets = make(map[string][]byte, len(secretEntries))
		for _, entry := range secretEntries {
			execReq.BrokeredSecrets[entry.GetSecretKey()] = append([]byte(nil), entry.GetSecretBytes()...)
		}
	}

	if grant := request.GetNetworkProxyGrant(); grant != nil && grant.GetLoopbackProxyAddress() != "" {
		httpClient, err := pluginproxy.NewProxyHTTPClient(
			grant.GetLoopbackProxyAddress(),
			grant.GetProxyAuthenticationToken(),
			defaultPluginHTTPClientTimeout,
		)
		if err != nil {
			return nil, fmt.Errorf("configure plugin proxy HTTP client: %w", err)
		}
		execReq.PluginHTTPClient = httpClient
	}
	return execReq, nil
}

func runSDKStatus(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
) (*plugincontract.PluginInvocationResult, error) {
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyOptional)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	resp, err := trawler.Status(ctx, execReq)
	if err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_Status{Status: resp},
	}, nil
}

func runSDKUpdate(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	op *plugincontract.PluginUpdateOperationRequest,
	writer *serializedPluginFrameWriter,
) (*plugincontract.PluginInvocationResult, error) {
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadWrite)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	report, err := trawlkit.ExecuteUpdateForExternalPluginSDK(ctx, trawler, execReq, op.GetUpdateArguments())
	if err != nil {
		return nil, err
	}
	if _, err := streamArchiveIndexChangesIfSupported(ctx, trawler, execReq, op.GetSinceOpaqueIndexCursor(), writer); err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_Update{Update: report},
	}, nil
}

func runSDKSearch(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	op *plugincontract.PluginSearchOperationRequest,
) (*plugincontract.PluginInvocationResult, error) {
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyRequired)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	query := trawlkit.Query{
		Text:  op.GetQueryText(),
		Limit: int(op.GetResultLimit()),
		SearchTotalIsLowerBoundWhenResultLimitIsReached: op.GetSearchTotalIsLowerBoundWhenResultLimitIsReached(),
	}
	if op.GetAfterTimeIsSet() && op.GetAfterTime() != nil {
		query.After = op.GetAfterTime().AsTime()
	}
	if op.GetBeforeTimeIsSet() && op.GetBeforeTime() != nil {
		query.Before = op.GetBeforeTime().AsTime()
	}
	if len(op.GetExactPersonFilterIdentifiers()) > 0 {
		query.PersonFilter = trawlkit.NewResolvedSearchPersonFilter(op.GetPersonFilterText(), op.GetExactPersonFilterIdentifiers())
	} else if op.GetPersonFilterText() != "" {
		query.PersonFilter = trawlkit.NewUnresolvedSearchPersonFilter(op.GetPersonFilterText())
	}
	resp, err := trawlkit.ExecuteSearchForExternalPluginSDK(ctx, trawler, execReq, query)
	if err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_Search{Search: resp},
	}, nil
}

func runSDKOpenRecord(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	op *plugincontract.PluginOpenRecordOperationRequest,
) (*plugincontract.PluginInvocationResult, error) {
	opener, ok := trawler.(trawlkit.CanonicalRecordOpener)
	if !ok {
		return nil, errors.New("plugin trawler does not implement CanonicalRecordOpener")
	}
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyRequired)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	if conversationLister, ok := trawler.(trawlkit.ConversationLister); ok {
		conversationRecord, err := trawlkit.FindConversationRecordWithCanonicalReference(
			ctx, conversationLister, execReq, trawler, op.GetCanonicalRecordReference(),
		)
		if err != nil {
			return nil, err
		}
		if conversationRecord != nil {
			return &plugincontract.PluginInvocationResult{
				Success: &plugincontract.PluginInvocationResult_OpenRecord{OpenRecord: &open.OpenRecord{
					RecordTrawler:            trawler.RegisteredTrawlerDeclaration().RegisteredTrawler,
					CanonicalRecordReference: conversationRecord.GetCanonicalRecordReference(),
					TypedOpenedRecord: &open.OpenRecord_ConversationRecord{
						ConversationRecord: conversationRecord,
					},
				}},
			}, nil
		}
	}
	execReq.RequestedRecordAnchor = op.GetRequestedRecordAnchor()
	record, err := opener.OpenCanonicalRecord(ctx, execReq, op.GetCanonicalRecordReference())
	if err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_OpenRecord{OpenRecord: record},
	}, nil
}

func runSDKExportArchiveIndexChanges(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	op *plugincontract.PluginExportArchiveIndexChangesOperationRequest,
	writer *serializedPluginFrameWriter,
) (*plugincontract.PluginInvocationResult, error) {
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyOptional)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	exportedAny, err := streamArchiveIndexChangesIfSupported(ctx, trawler, execReq, op.GetSinceOpaqueIndexCursor(), writer)
	if err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_ArchiveIndexExportCompleted{
			ArchiveIndexExportCompleted: &plugincontract.PluginArchiveIndexExportCompleted{
				ExportedAnyBatches: exportedAny,
			},
		},
	}, nil
}

func runSDKBespokeCommand(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	op *plugincontract.PluginBespokeCommandOperationRequest,
	writer *serializedPluginFrameWriter,
) (*plugincontract.PluginInvocationResult, error) {
	resp, mutated, err := trawlkit.ExecuteBespokeCommandForExternalPluginSDK(ctx, trawler, op.GetCommandArguments(), execReq)
	if err != nil {
		return nil, err
	}
	if mutated {
		opened, openErr := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyOptional)
		if openErr != nil {
			return nil, openErr
		}
		if opened != nil {
			defer func() { _ = opened.Close() }()
			execReq.OpenedTrawlerArchiveStore = opened
			if _, err := streamArchiveIndexChangesIfSupported(ctx, trawler, execReq, op.GetSinceOpaqueIndexCursor(), writer); err != nil {
				return nil, err
			}
		}
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_TrawlerCommandResponse{TrawlerCommandResponse: resp},
	}, nil
}

func runSDKWho(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	op *plugincontract.PluginWhoOperationRequest,
) (*plugincontract.PluginInvocationResult, error) {
	matcher, ok := trawler.(trawlkit.WhoMatcher)
	if !ok {
		return nil, errors.New("plugin trawler does not support who")
	}
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyRequired)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	resp, err := matcher.Who(ctx, execReq, op.GetQueryText())
	if err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_Who{Who: resp},
	}, nil
}

func runSDKCountPersonMessages(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	personFilter *person.ExactPersonMessageCountRequest,
) (*plugincontract.PluginInvocationResult, error) {
	counter, ok := trawler.(trawlkit.PersonMessageCounter)
	if !ok {
		return nil, errors.New("plugin trawler does not support person message counts")
	}
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyRequired)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	resp, err := counter.CountPersonMessages(ctx, execReq, personFilter)
	if err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_CountPersonMessages{CountPersonMessages: resp},
	}, nil
}

func runSDKConversations(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	op *plugincontract.PluginConversationsOperationRequest,
) (*plugincontract.PluginInvocationResult, error) {
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyRequired)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	resp, err := trawlkit.ExecuteConversationsForExternalPluginSDK(ctx, trawler, execReq, trawlkit.ConversationQuery{
		Limit:  int(op.GetResultLimit()),
		All:    op.GetIncludeAllConversations(),
		Unread: op.GetUnreadOnly(),
		PersonConversationFilterResolvedAcrossTrawlerArchives: op.GetResolvedPersonConversationFilter(),
	})
	if err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_Conversations{Conversations: resp},
	}, nil
}

func runSDKMessages(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	op *plugincontract.PluginMessagesOperationRequest,
) (*plugincontract.PluginInvocationResult, error) {
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyRequired)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	query := trawlkit.TrawlerMessageListQuery{
		OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation: trawlkit.LocalTrawlerShortReferenceText(op.GetLocalConversationShortReference()),
		MaximumReturnedMessageCount: int(op.GetResultLimit()),
	}
	if op.GetAfterTimeIsSet() && op.GetAfterTime() != nil {
		query.After = op.GetAfterTime().AsTime()
	}
	if op.GetBeforeTimeIsSet() && op.GetBeforeTime() != nil {
		query.Before = op.GetBeforeTime().AsTime()
	}
	resp, err := trawlkit.ExecuteMessagesForExternalPluginSDK(ctx, trawler, execReq, query)
	if err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_Messages{Messages: resp},
	}, nil
}

func runSDKPeopleSnapshot(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
) (*plugincontract.PluginInvocationResult, error) {
	provider, ok := trawler.(trawlkit.PeopleSnapshotProvider)
	if !ok {
		return nil, errors.New("plugin trawler does not support people snapshots")
	}
	opened, err := trawlkit.OpenExternalPluginArchiveStore(ctx, execReq.TrawlerArchivePaths.TrawlerArchivePath, trawlkit.ArchiveStoreAccessReadOnlyRequired)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
		execReq.OpenedTrawlerArchiveStore = opened
	}
	snapshot, err := provider.PeopleSnapshot(ctx, execReq)
	if err != nil {
		return nil, err
	}
	return &plugincontract.PluginInvocationResult{
		Success: &plugincontract.PluginInvocationResult_PeopleSnapshot{PeopleSnapshot: snapshot},
	}, nil
}

func streamArchiveIndexChangesIfSupported(
	ctx context.Context,
	trawler trawlkit.Trawler,
	execReq *trawlkit.TrawlerCommandExecutionRequest,
	sinceCursor string,
	writer *serializedPluginFrameWriter,
) (bool, error) {
	exporter, ok := trawler.(trawlkit.ArchiveIndexChangeExporter)
	if !ok || execReq.OpenedTrawlerArchiveStore == nil {
		return false, nil
	}
	exportedAny := false
	err := exporter.ExportArchiveIndexChanges(ctx, execReq, sinceCursor, func(batch *indexcontract.ArchiveIndexChangeBatch) error {
		if batch == nil {
			return nil
		}
		exportedAny = true
		return writer.WriteFrame(&plugincontract.PluginStreamFrame{
			Kind: &plugincontract.PluginStreamFrame_ArchiveIndexChangeBatch{
				ArchiveIndexChangeBatch: batch,
			},
		})
	})
	return exportedAny, err
}
