package trawlkit

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func renderStatus(status *control.Status) render.Status {
	if status == nil {
		return render.Status{State: render.StatusUnknown, Summary: "No status returned."}
	}
	out := render.Status{
		State:    render.StatusState(status.State),
		Summary:  statusSummary(status),
		Warnings: append([]string(nil), status.Warnings...),
		Errors:   append([]string(nil), status.Errors...),
	}
	if len(status.Counts) > 0 {
		archiveFields := archiveStatusFields(status)
		if len(archiveFields) > 0 {
			out.Sections = append(out.Sections, render.Section{Title: "Local archive", Fields: archiveFields})
		}
	} else {
		var archiveFields []render.Field
		if status.ConfigPath != "" {
			archiveFields = append(archiveFields, render.Field{Label: "Config", Value: status.ConfigPath})
		}
		if status.DatabasePath != "" {
			archiveFields = append(archiveFields, render.Field{Label: "Database", Value: status.DatabasePath})
		}
		if status.DatabaseBytes > 0 {
			archiveFields = append(archiveFields, render.Field{Label: "Database size", Value: render.FormatInteger(status.DatabaseBytes) + " bytes"})
		}
		if status.WALBytes > 0 {
			archiveFields = append(archiveFields, render.Field{Label: "WAL size", Value: render.FormatInteger(status.WALBytes) + " bytes"})
		}
		if len(archiveFields) > 0 {
			out.Sections = append(out.Sections, render.Section{Title: "Archive", Fields: archiveFields})
		}
	}
	if status.LastSyncAt == "" && status.LastImportAt != "" {
		out.Freshness = &render.Freshness{Label: "Last import", LastSync: status.LastImportAt}
	}
	if status.Freshness != nil && status.Freshness.Status != "" {
		if out.Freshness == nil {
			out.Freshness = &render.Freshness{}
		}
		out.Freshness.State = status.Freshness.Status
	}
	return out
}

func archiveStatusFields(status *control.Status) []render.Field {
	var fields []render.Field
	if status.ConfigPath != "" {
		fields = append(fields, render.Field{Label: "Config", Value: status.ConfigPath})
	}
	if status.DatabasePath != "" {
		fields = append(fields, render.Field{Label: "Database", Value: status.DatabasePath})
	}
	if status.LastSyncAt != "" {
		fields = append(fields, render.Field{Label: "Last sync", Value: shortStatusTime(status.LastSyncAt)})
	}
	for _, count := range status.Counts {
		label := firstText(count.Label, count.ID)
		fields = append(fields, render.Field{Label: displayFieldLabel(label), Value: render.FormatCount(count.Value, count.ID, label)})
	}
	return fields
}

func statusSummary(status *control.Status) string {
	if status == nil {
		return "No status returned."
	}
	switch strings.TrimSpace(status.State) {
	case "ok":
		return "Recently synced."
	case "stale":
		if strings.TrimSpace(status.Summary) != "" {
			return status.Summary
		}
		return "Needs sync."
	default:
		return status.Summary
	}
}

func displayFieldLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func shortStatusTime(value string) string {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return value
	}
	return render.ShortLocalTime(t)
}
