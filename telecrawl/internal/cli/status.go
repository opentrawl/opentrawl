package cli

import (
	"flag"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/render"
)

func (r *runtime) runStatus(args []string) error {
	fs := flag.NewFlagSet("telecrawl status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	return r.print(r.statusEnvelope())
}

func (r *runtime) printStatus(value statusEnvelope) error {
	return render.WriteStatus(r.stdout, render.Status{
		State:   render.StatusState(value.State),
		Summary: value.Summary,
		Sections: []render.Section{
			{Title: "Archive", Fields: statusRenderFields(value.Counts)},
			{Title: "Auth", Fields: authRenderFields(value.Auth)},
		},
		Freshness: statusRenderFreshness(value.Freshness),
	})
}

func statusRenderFields(counts []countEnvelope) []render.Field {
	fields := make([]render.Field, 0, len(counts))
	for _, count := range counts {
		label := statusCountLabel(count.ID, count.Label)
		display := groupDigits64(count.Value)
		if count.ID == "since" {
			if count.Value == 0 {
				display = "not available"
			} else {
				display = strconv.FormatInt(count.Value, 10)
			}
		}
		fields = append(fields, render.Field{Label: label, Value: display})
	}
	return fields
}

func authRenderFields(auth authEnvelope) []render.Field {
	fields := []render.Field{{Label: "Authorised", Value: strconv.FormatBool(auth.Authorized)}}
	if auth.Expires != nil {
		fields = append(fields, render.Field{Label: "Expires", Value: *auth.Expires})
	}
	return fields
}

func statusRenderFreshness(freshness freshnessEnvelope) *render.Freshness {
	if freshness.LastSync == "" {
		return nil
	}
	return &render.Freshness{LastSync: shortLocalTime(parseRenderTime(freshness.LastSync)), Label: "Last import"}
}

func statusCountLabel(id, fallback string) string {
	switch id {
	case "messages":
		return "Messages"
	case "chats":
		return "Chats"
	case "since":
		return "First message"
	default:
		return humanLabel(fallback)
	}
}

func humanLabel(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
