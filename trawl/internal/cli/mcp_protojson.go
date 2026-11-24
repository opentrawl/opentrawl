package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var mcpProtoJSONMarshalOptions = protojson.MarshalOptions{
	UseProtoNames:   false,
	EmitUnpopulated: false,
}

type mcpAroundMatchJSON struct {
	When    string `json:"when,omitempty"`
	How     string `json:"how,omitempty"`
	Source  string `json:"source,omitempty"`
	Summary string `json:"summary,omitempty"`
	Link    string `json:"link,omitempty"`
}

type mcpAroundResultJSON struct {
	AnchorSummary string               `json:"anchorSummary,omitempty"`
	AnchorLink    string               `json:"anchorLink,omitempty"`
	WindowStart   string               `json:"windowStart,omitempty"`
	WindowEnd     string               `json:"windowEnd,omitempty"`
	TotalFound    int                  `json:"totalFound"`
	Matches       []mcpAroundMatchJSON `json:"matches"`
}

// executeMCPToolCall runs the exact same typed command execution path as the
// CLI, projecting its protobuf result to ProtoJSON for both content[0].text
// and MCP structuredContent.
func (service *agentArchiveService) executeMCPToolCall(
	ctx context.Context,
	scope agentSourceScope,
	toolName string,
	arguments mcpToolArguments,
) (mcpToolCallResult, bool) {
	r := service.newAgentRequestRuntime(ctx, scope)
	limitOrDefault := func(defaultLimit int) int {
		if arguments.Limit > 0 {
			return arguments.Limit
		}
		return defaultLimit
	}
	switch toolName {
	case "trawl_status":
		cmd := &StatusCmd{Trawler: strings.TrimSpace(arguments.Trawler)}
		response, err := cmd.execute(r)
		if err != nil {
			return mcpErrorToolCallResult(mcpErrorMessageText(err)), true
		}
		encoded, marshalErr := marshalMCPProtoJSON(response)
		failed := response.GetOutcome() == federationcontract.OperationOutcome_OPERATION_OUTCOME_FAILED &&
			len(response.GetTrawlerStatusResults()) == 0
		return mcpToolResultFromOutcome(encoded, failed, marshalErr), true

	case "trawl_search":
		cmd := &SearchCmd{
			Query:   strings.Fields(arguments.Query),
			Trawler: arguments.Trawler,
			Limit:   limitOrDefault(20),
			After:   arguments.After,
			Before:  arguments.Before,
			Who:     arguments.Who,
		}
		executed, err := cmd.execute(r)
		if err != nil {
			return mcpErrorToolCallResult(mcpErrorMessageText(err)), true
		}
		projected := projectFederatedSearchOperationForMCP(executed.response)
		encoded, marshalErr := marshalMCPProtoJSON(projected)
		failed := projected.GetOutcome() == federationcontract.OperationOutcome_OPERATION_OUTCOME_FAILED &&
			len(projected.GetSearchMatchesInDisplayOrder()) == 0
		return mcpToolResultFromOutcome(encoded, failed, marshalErr), true

	case "trawl_open":
		cmd := &OpenCmd{Link: strings.TrimSpace(arguments.Link)}
		response, _, err := cmd.execute(r)
		if err != nil {
			return mcpErrorToolCallResult(mcpErrorMessageText(err)), true
		}
		encoded, marshalErr := marshalMCPProtoJSON(response)
		failed := response.GetFailure() != nil || response.GetRecord() == nil
		return mcpToolResultFromOutcome(encoded, failed, marshalErr), true

	case "trawl_who":
		cmd := &WhoCmd{
			Name:  strings.Fields(arguments.Name),
			Limit: limitOrDefault(20),
		}
		executed, err := cmd.execute(r)
		if err != nil {
			return mcpErrorToolCallResult(mcpErrorMessageText(err)), true
		}
		encoded, marshalErr := marshalMCPProtoJSON(executed.operation)
		failed := len(executed.operation.GetOperationFailures()) > 0 &&
			len(executed.operation.GetPersonMatchCandidates()) == 0
		return mcpToolResultFromOutcome(encoded, failed, marshalErr), true

	case "trawl_around":
		cmd := &AroundCmd{
			Target:  strings.TrimSpace(arguments.Target),
			Trip:    strings.TrimSpace(arguments.Trip),
			Window:  arguments.Window,
			Trawler: arguments.Trawler,
			Limit:   limitOrDefault(30),
		}
		result, err := cmd.execute(r)
		if err != nil {
			return mcpErrorToolCallResult(mcpErrorMessageText(err)), true
		}
		matches := make([]mcpAroundMatchJSON, 0, len(result.Matches))
		for _, match := range result.Matches {
			matches = append(matches, mcpAroundMatchJSON{
				When:    formatAroundMatchTime(match),
				How:     match.JoinReason,
				Source:  match.TrawlerDisplay,
				Summary: formatAroundMatchSummary(match),
				Link:    match.Link,
			})
		}
		payload := mcpAroundResultJSON{
			AnchorSummary: result.AnchorSummary,
			AnchorLink:    result.AnchorLink,
			TotalFound:    result.TotalFound,
			Matches:       matches,
		}
		if !result.WindowStart.IsZero() {
			payload.WindowStart = result.WindowStart.Local().Format("2006-01-02T15:04:05-07:00")
		}
		if !result.WindowEnd.IsZero() {
			payload.WindowEnd = result.WindowEnd.Local().Format("2006-01-02T15:04:05-07:00")
		}
		encoded, marshalErr := json.MarshalIndent(payload, "", "  ")
		return mcpToolResultFromOutcome(encoded, false, marshalErr), true

	default:
		return mcpToolCallResult{}, false
	}
}

func mcpToolResultFromOutcome(payload []byte, failed bool, err error) mcpToolCallResult {
	if err != nil {
		return mcpErrorToolCallResult(mcpErrorMessageText(err))
	}
	trimmed := strings.TrimSpace(string(payload))
	return mcpToolCallResult{
		Content:           []mcpToolTextContent{{Type: "text", Text: trimmed}},
		StructuredContent: json.RawMessage(trimmed),
		IsError:           failed,
	}
}

func mcpErrorToolCallResult(message string) mcpToolCallResult {
	encoded, err := json.MarshalIndent(mcpHTTPErrorResponse{Error: message}, "", "  ")
	if err != nil {
		return mcpToolCallResult{
			Content: []mcpToolTextContent{{Type: "text", Text: message}},
			IsError: true,
		}
	}
	return mcpToolCallResult{
		Content:           []mcpToolTextContent{{Type: "text", Text: string(encoded)}},
		StructuredContent: json.RawMessage(encoded),
		IsError:           true,
	}
}

func mcpErrorMessageText(err error) string {
	if failure, worded := render.AsCommandFailure(err); worded {
		return strings.TrimSpace(failure.Error())
	}
	return strings.TrimSpace(err.Error())
}

func projectFederatedSearchOperationForMCP(
	source *federationcontract.FederatedTrawlerSearchOperation,
) *federationcontract.FederatedTrawlerSearchOperation {
	cloned, _ := proto.Clone(source).(*federationcontract.FederatedTrawlerSearchOperation)
	if cloned == nil {
		return &federationcontract.FederatedTrawlerSearchOperation{}
	}
	for _, trawlerResult := range cloned.GetTrawlerSearchResults() {
		trawlerResult.SearchMatchesFromTrawlerInDisplayOrder = nil
		trawlerResult.EmptySearchNextCommands = nil
	}
	for _, match := range cloned.GetSearchMatchesInDisplayOrder() {
		if presentation := match.GetSearchMatchPresentation(); presentation != nil {
			render.PrepareSearchMatchMonetaryAmountForApp(presentation)
			if presentation.GetAppleLibraryPhotoFacts() != nil {
				presentation.PhysicalPlaceNamesSpecificToBroadest = render.SearchMatchPhysicalPlaceNamesForHumanOutput(presentation)
			}
		}
	}
	return cloned
}

func marshalMCPProtoJSON(message proto.Message) ([]byte, error) {
	cloned := proto.Clone(message)
	stripInternalIdentifiersFromProtoMessage(cloned.ProtoReflect())
	compact, err := mcpProtoJSONMarshalOptions.Marshal(cloned)
	if err != nil {
		return nil, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return compact, nil
	}
	return pretty.Bytes(), nil
}

// stripInternalIdentifiersFromProtoMessage removes internal database record
// references, anchors, and exact filter handles recursively so MCP ProtoJSON
// exposes only human/agent-safe links and fields.
func stripInternalIdentifiersFromProtoMessage(message protoreflect.Message) {
	if !message.IsValid() {
		return
	}
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Kind() != protoreflect.MessageKind {
			return true
		}
		if isInternalProtoIdentifierMessage(field.Message().FullName()) {
			message.Clear(field)
			return true
		}
		if field.IsList() {
			list := value.List()
			for i := 0; i < list.Len(); i++ {
				stripInternalIdentifiersFromProtoMessage(list.Get(i).Message())
			}
			return true
		}
		if field.IsMap() {
			if field.MapValue().Kind() == protoreflect.MessageKind {
				value.Map().Range(func(_ protoreflect.MapKey, mapValue protoreflect.Value) bool {
					stripInternalIdentifiersFromProtoMessage(mapValue.Message())
					return true
				})
			}
			return true
		}
		stripInternalIdentifiersFromProtoMessage(value.Message())
		return true
	})
}

func isInternalProtoIdentifierMessage(fullName protoreflect.FullName) bool {
	switch fullName {
	case "trawl.identity.CanonicalArchiveRecordReference",
		"trawl.identity.RecordAnchorIdentifier",
		"trawl.person.ExactPersonFilterIdentifier":
		return true
	default:
		return false
	}
}
