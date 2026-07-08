package telecrawl

import (
	"context"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/store"
)

func (c *Crawler) Status(ctx context.Context, req *crawlkit.Request) (*control.Status, error) {
	status := control.NewStatus(appID, "archive database is missing")
	status.State = "missing"
	status.DatabasePath = req.Paths.Archive
	if req.Store == nil {
		return &status, nil
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		status.State = "error"
		status.Summary = "archive database cannot be read"
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	defer func() { _ = st.Close() }()
	archiveStatus, err := st.Status(ctx)
	if err != nil {
		status.State = "error"
		status.Summary = "archive status cannot be read"
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	status.LastImportAt = formatOptionalTime(archiveStatus.LastImportAt)
	status.Counts = []control.Count{
		control.NewCount("messages", "messages", int64(archiveStatus.Messages)),
		control.NewCount("chats", "chats", int64(archiveStatus.Chats)),
		control.NewCount("since", "since", oldestMessageYear(archiveStatus)),
	}
	status.State = statusState(archiveStatus)
	status.Summary = statusSummary(archiveStatus)
	return &status, nil
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
	return &render.Freshness{LastSync: shortLocalTime(parseRenderTime(freshness.LastSync)), Label: "Last sync"}
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
