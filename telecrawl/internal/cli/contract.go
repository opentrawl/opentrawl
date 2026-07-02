package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

const (
	defaultMessageLimit = 50
	defaultSearchLimit  = 20
	maxSearchLimit      = 200

	// Telegram archives older than a day are stale for status surfaces that imply source parity.
	statusFreshFor = 24 * time.Hour
)

type metadataEnvelope struct {
	SchemaVersion   int      `json:"schema_version"`
	ContractVersion int      `json:"contract_version"`
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name"`
	Version         string   `json:"version"`
	Capabilities    []string `json:"capabilities"`
}

type statusEnvelope struct {
	AppID     string            `json:"app_id"`
	State     string            `json:"state"`
	Summary   string            `json:"summary"`
	Freshness freshnessEnvelope `json:"freshness"`
	Counts    []countEnvelope   `json:"counts"`
	Auth      authEnvelope      `json:"auth"`
}

type freshnessEnvelope struct {
	LastSync string `json:"last_sync,omitempty"`
}

type countEnvelope struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value int64  `json:"value"`
}

type authEnvelope struct {
	Authorized bool    `json:"authorized"`
	Expires    *string `json:"expires"`
}

type doctorOutput struct {
	Checks []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

type searchEnvelope struct {
	Query        string         `json:"query"`
	Results      []searchResult `json:"results"`
	TotalMatches int            `json:"total_matches"`
	Truncated    bool           `json:"truncated"`
}

type searchResult struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}

func contractMetadata() metadataEnvelope {
	return metadataEnvelope{
		SchemaVersion:   1,
		ContractVersion: 1,
		ID:              "telecrawl",
		DisplayName:     "Telegram",
		Version:         version,
		Capabilities:    []string{"metadata", "doctor", "status", "sync", "search", "contacts_export", "backup"},
	}
}

func (r *runtime) statusEnvelope() statusEnvelope {
	if info, err := os.Stat(r.dbPath); err != nil {
		if os.IsNotExist(err) {
			return newStatusEnvelope("missing", "archive database is missing", store.Status{})
		}
		return newStatusEnvelope("error", "archive database cannot be read", store.Status{})
	} else if info.IsDir() {
		return newStatusEnvelope("error", "archive database path is a directory", store.Status{})
	}
	st, err := store.Open(r.ctx, r.dbPath)
	if err != nil {
		return newStatusEnvelope("error", "archive database cannot be read", store.Status{})
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(r.ctx)
	if err != nil {
		return newStatusEnvelope("error", "archive status cannot be read", store.Status{})
	}
	return newStatusEnvelope(statusState(status), statusSummary(status), status)
}

func newStatusEnvelope(state, summary string, status store.Status) statusEnvelope {
	return statusEnvelope{
		AppID:     "telecrawl",
		State:     state,
		Summary:   summary,
		Freshness: statusFreshness(status),
		Counts: []countEnvelope{
			{ID: "messages", Label: "messages", Value: int64(status.Messages)},
			{ID: "chats", Label: "chats", Value: int64(status.Chats)},
			{ID: "since", Label: "since", Value: oldestMessageYear(status)},
		},
		Auth: authEnvelope{Authorized: true},
	}
}

func statusState(status store.Status) string {
	switch {
	case status.Messages == 0:
		return "empty"
	case status.LastImportAt.IsZero():
		return "stale"
	case time.Since(status.LastImportAt) > statusFreshFor:
		return "stale"
	default:
		return "ok"
	}
}

func statusSummary(status store.Status) string {
	switch statusState(status) {
	case "empty":
		return "archive is empty"
	case "stale":
		return "archive sync is stale"
	default:
		return "archive is fresh"
	}
}

func statusFreshness(status store.Status) freshnessEnvelope {
	if status.LastImportAt.IsZero() {
		return freshnessEnvelope{}
	}
	return freshnessEnvelope{LastSync: status.LastImportAt.Format(time.RFC3339)}
}

func oldestMessageYear(status store.Status) int64 {
	if status.OldestMessage.IsZero() {
		return 0
	}
	return int64(status.OldestMessage.Year())
}

func (r *runtime) doctorEnvelope(report telegramdesktop.Report) doctorOutput {
	return doctorOutput{Checks: []doctorCheck{sourceStoreCheck(report), r.archiveCheck()}}
}

func sourceStoreCheck(report telegramdesktop.Report) doctorCheck {
	if report.Exists && report.Accessible && report.Error == "" {
		return doctorCheck{ID: "source_store", State: "ok"}
	}
	check := doctorCheck{
		ID:     "source_store",
		State:  "fail",
		Remedy: "Install or open Telegram Desktop, or pass --path to a readable Telegram data directory.",
	}
	switch {
	case !report.Exists:
		check.Message = "Telegram source data was not found."
	case report.Error != "":
		check.Message = "Telegram source data could not be read."
	default:
		check.Message = "Telegram source data is not readable."
	}
	return check
}

func (r *runtime) archiveCheck() doctorCheck {
	if info, err := os.Stat(r.dbPath); err != nil {
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "telecrawl archive has not been created.",
			Remedy:  "Run telecrawl import to create the archive.",
		}
	} else if info.IsDir() {
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "telecrawl archive path is a directory.",
			Remedy:  "Pass --db with a sqlite archive path, then run telecrawl import.",
		}
	}
	st, err := store.Open(r.ctx, r.dbPath)
	if err != nil {
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "telecrawl archive cannot be read.",
			Remedy:  "Run telecrawl import to rebuild the archive.",
		}
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Status(r.ctx); err != nil {
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "telecrawl archive status cannot be read.",
			Remedy:  "Run telecrawl import to rebuild the archive.",
		}
	}
	return doctorCheck{ID: "archive", State: "ok"}
}

func newSearchEnvelope(query string, messages []store.Message, total int) searchEnvelope {
	return searchEnvelope{
		Query:        query,
		Results:      searchResults(messages),
		TotalMatches: total,
		Truncated:    total > len(messages),
	}
}

func searchResults(messages []store.Message) []searchResult {
	out := make([]searchResult, 0, len(messages))
	for _, message := range messages {
		out = append(out, searchResult{
			Ref:     fmt.Sprintf("telecrawl:msg/%d", message.SourcePK),
			Time:    message.Timestamp.Format(time.RFC3339),
			Who:     messageWho(message),
			Where:   messageWhere(message),
			Snippet: messageSnippet(message),
		})
	}
	return out
}

func messageWho(message store.Message) string {
	if value := strings.TrimSpace(message.SenderName); value != "" {
		return value
	}
	if message.FromMe {
		return "me"
	}
	if value := strings.TrimSpace(message.SenderJID); value != "" {
		return value
	}
	return "unknown"
}

func messageWhere(message store.Message) string {
	if value := strings.TrimSpace(message.ChatName); value != "" {
		return value
	}
	if value := strings.TrimSpace(message.ChatJID); value != "" {
		return value
	}
	return "unknown"
}

func messageSnippet(message store.Message) string {
	if value := strings.TrimSpace(message.Snippet); value != "" {
		return value
	}
	return strings.TrimSpace(message.Text)
}
