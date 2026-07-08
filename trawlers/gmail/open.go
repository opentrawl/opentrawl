package gogcrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

const maxOpenBodyRunes = 4000

type openOutput struct {
	archive.OpenResult
	shortRef string
}

func (c *Crawler) Open(ctx context.Context, req *crawlkit.Request, ref string) error {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(err)
	}
	resolved, err := c.resolveOpenRef(ctx, req, ref)
	if err != nil {
		return err
	}
	result, err := st.OpenMessage(ctx, resolved)
	if err != nil {
		return commandErr("message_not_found", "message could not be opened", "search again and pass a gmail:msg ref", err)
	}
	result = boundOpenResult(result)
	_ = logInfo(req, "open_complete", "result=message")
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "open", result)
	}
	return printOpenText(req.Out, openOutput{OpenResult: result, shortRef: openShortRef(ctx, req, result.Ref)})
}

func (c *Crawler) resolveOpenRef(ctx context.Context, req *crawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		return ref, nil
	}
	matches, err := req.ResolveShortRef(ctx, ref)
	if errors.Is(err, crawlkit.ErrUnknownShortRef) {
		return "", commandErr("unknown_short_ref", "short ref is unknown", "use a full gmail:msg ref", err)
	}
	if errors.Is(err, crawlkit.ErrAmbiguousShortRef) {
		return "", commandErr("ambiguous_short_ref", "short ref is ambiguous", "rerun search or use the full gmail:msg ref", err)
	}
	if err != nil {
		return "", err
	}
	return matches[0], nil
}

func openShortRef(ctx context.Context, req *crawlkit.Request, ref string) string {
	aliases, err := req.ShortRefAliases(ctx, []string{ref})
	if err != nil {
		return ""
	}
	return aliases[ref]
}

func boundOpenResult(result archive.OpenResult) archive.OpenResult {
	body, elided := truncateOpenBody(result.Body)
	result.Body = body
	result.BodyTruncated = elided > 0
	result.BodyElidedChars = elided
	return result
}

func truncateOpenBody(body string) (string, int) {
	runes := []rune(body)
	if len(runes) <= maxOpenBodyRunes {
		return body, 0
	}
	return string(runes[:maxOpenBodyRunes]), len(runes) - maxOpenBodyRunes
}

func printOpenText(w io.Writer, value openOutput) error {
	title := strings.TrimSpace(value.Headers.Subject)
	if title == "" {
		title = "(no subject)"
	}
	hints := make([]string, 0, 2)
	if value.BodyTruncated {
		hints = append(hints, fmt.Sprintf("... %s more characters. Open the full message in Gmail.", commaInt(value.BodyElidedChars)))
	}
	hints = append(hints, "JSON: trawl gmail open REF --json for the full record.")
	return render.WriteCard(w, render.Card{
		Title: title,
		Fields: []render.CardField{
			{Label: "Date", Value: render.ShortLocalTime(parseOpenTime(value.Time))},
			{Label: "From", Value: senderText(value.Headers)},
			{Label: "To", Value: value.Headers.ToAddress},
			{Label: "Cc", Value: value.Headers.CcAddress},
			{Label: "Attachments", Value: attachmentsLine(value.Attachments)},
			{Label: "Ref", Value: value.shortRef},
		},
		Body:  value.Body,
		Hints: hints,
	})
}

func parseOpenTime(value string) time.Time {
	parsed, err := parseContractTime(value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func attachmentsLine(attachments []archive.Attachment) string {
	parts := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		name := strings.TrimSpace(attachment.Filename)
		if name == "" {
			name = "(unnamed)"
		}
		parts = append(parts, fmt.Sprintf("%s (%s bytes)", name, commaInt(int(attachment.Size))))
	}
	return strings.Join(parts, ", ")
}

func senderText(headers archive.MailHeaders) string {
	if headers.FromName != "" && headers.FromAddress != "" {
		return headers.FromName + " <" + headers.FromAddress + ">"
	}
	if headers.FromName != "" {
		return headers.FromName
	}
	return headers.FromAddress
}

func commaInt(value int) string {
	raw := strconv.Itoa(value)
	if len(raw) <= 3 {
		return raw
	}
	head := len(raw) % 3
	if head == 0 {
		head = 3
	}
	out := raw[:head]
	for i := head; i < len(raw); i += 3 {
		out += "," + raw[i:i+3]
	}
	return out
}
