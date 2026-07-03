package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/imsgcrawl/internal/archive"
)

const messageRefPrefix = archive.MessageRefPrefix

var (
	errForeignRef = errors.New("ref is not from imsgcrawl")
	errInvalidRef = errors.New("ref is not an imsgcrawl message ref")
)

type errorEnvelope struct {
	Error commandError `json:"error"`
}

type commandError struct {
	Code            string                  `json:"code"`
	Message         string                  `json:"message"`
	Remedy          string                  `json:"remedy"`
	Candidates      []archive.WhoCandidate  `json:"candidates,omitempty"`
	CandidateTotal  int                     `json:"candidate_total,omitempty"`
	DidYouMean      *[]archive.WhoCandidate `json:"did_you_mean,omitempty"`
	DidYouMeanTotal int                     `json:"did_you_mean_total,omitempty"`
	Hint            string                  `json:"hint,omitempty"`
}

func (r *runtime) runOpen(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"open"})
	}
	fs := flag.NewFlagSet("imsgcrawl open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 1 {
		return usageErr(errors.New("open takes exactly one ref"))
	}
	ref := strings.TrimSpace(fs.Arg(0))
	if !strings.Contains(ref, ":") {
		return r.openShortRef(ref)
	}
	messageID, err := parseMessageRef(ref)
	if err != nil {
		if errors.Is(err, errForeignRef) {
			return r.contractError("foreign_ref", "ref is not from imsgcrawl", "use a ref returned by imsgcrawl search --json")
		}
		return r.contractError("invalid_ref", "ref is not an imsgcrawl message ref", "use a ref in the form imsgcrawl:msg/ID")
	}
	return r.withArchive(func(st *archive.Store) error {
		result, err := st.OpenMessage(r.ctx, messageID, defaultOpenWindow)
		if errors.Is(err, archive.ErrMessageNotFound) {
			return r.contractError("not_found", "message ref was not found", "run imsgcrawl search --json again and use a current ref")
		}
		if err != nil {
			return err
		}
		return r.print(newOpenOutput(result))
	})
}

func (r *runtime) openShortRef(alias string) error {
	if !archive.ValidShortRef(alias) {
		return r.contractError("invalid_ref", "ref is not an imsgcrawl message ref", "use a ref in the form imsgcrawl:msg/ID or a short ref from search")
	}
	return r.withWritableArchive(func(st *archive.Store) error {
		resolved, err := st.ResolveShortRef(r.ctx, alias)
		if err != nil {
			return err
		}
		if resolved.Rebuilt {
			_ = r.logInfo("short_refs_rebuilt", "alias_index=rebuilt")
		}
		switch len(resolved.FullRefs) {
		case 0:
			return r.contractError("unknown_short_ref", "short ref was not found", "rerun search or use the full ref")
		case 1:
			messageID, err := parseMessageRef(resolved.FullRefs[0])
			if err != nil {
				return err
			}
			result, err := st.OpenMessage(r.ctx, messageID, defaultOpenWindow)
			if errors.Is(err, archive.ErrMessageNotFound) {
				return r.contractError("not_found", "message ref was not found", "run imsgcrawl search --json again and use a current ref")
			}
			if err != nil {
				return err
			}
			return r.print(newOpenOutput(result))
		default:
			return r.contractError("ambiguous_short_ref", "short ref matches more than one message", "rerun search or use the full ref")
		}
	})
}

func (r *runtime) contractError(code, message, remedy string) error {
	_ = r.logError(code, worldMustChange(nil, message, remedy))
	envelope := errorEnvelope{Error: commandError{Code: code, Message: message, Remedy: remedy}}
	if r.json {
		_ = r.print(envelope)
	} else {
		_, _ = fmt.Fprintf(r.stderr, "%s\nRemedy: %s\n", message, remedy)
	}
	return &cliError{code: 1, err: errors.New(message)}
}

func messageRef(messageID string) string {
	return archive.MessageRef(messageID)
}

func parseMessageRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, messageRefPrefix) {
		return "", errForeignRef
	}
	messageID := strings.TrimPrefix(ref, messageRefPrefix)
	if messageID == "" || strings.TrimSpace(messageID) != messageID {
		return "", errInvalidRef
	}
	id, err := strconv.ParseInt(messageID, 10, 64)
	if err != nil || id <= 0 {
		return "", errInvalidRef
	}
	return messageID, nil
}

func newOpenOutput(value archive.MessageContext) openOutput {
	where := chatDisplayName(value.Chat)
	if where == "" {
		where = "unknown chat"
	}
	where = outputField(where)
	out := openOutput{
		Ref: messageRef(value.Message.MessageID),
		Chat: openChatOutput{
			Name:         where,
			Participants: value.Chat.ParticipantHandles,
		},
		Message: openMessageItem(value.Message, where, false),
	}
	out.Context = make([]openMessageOutput, 0, len(value.Before)+1+len(value.After))
	for _, item := range value.Before {
		out.Context = append(out.Context, openMessageItem(item, where, false))
	}
	out.Context = append(out.Context, openMessageItem(value.Message, where, true))
	for _, item := range value.After {
		out.Context = append(out.Context, openMessageItem(item, where, false))
	}
	return out
}

func openMessageItem(item archive.MessageRow, where string, target bool) openMessageOutput {
	return openMessageOutput{
		Ref:            messageRef(item.MessageID),
		Time:           item.Time,
		Who:            outputField(senderName(item.FromMe, item.SenderLabel)),
		Where:          outputField(where),
		Text:           item.Text,
		FromMe:         item.FromMe,
		HasAttachments: item.HasAttachments,
		Target:         target,
	}
}
