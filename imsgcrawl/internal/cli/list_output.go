package cli

import (
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

const (
	defaultChatLimit    = 50
	defaultMessageLimit = 20
	defaultSearchLimit  = 20
	defaultOpenWindow   = 10
)

type listHeader struct {
	SchemaVersion string `json:"schema_version"`
	AppID         string `json:"app_id"`
	Command       string `json:"command"`
	Returned      int    `json:"returned"`
	Total         int64  `json:"total"`
	Limit         int    `json:"limit,omitempty"`
	Complete      bool   `json:"complete"`
}

type chatListOutput struct {
	listHeader
	Items []archive.ChatSummary `json:"items"`
}

type messageListOutput struct {
	listHeader
	ChatID string               `json:"chat_id"`
	Chat   *archive.ChatSummary `json:"chat,omitempty"`
	Order  string               `json:"order"`
	Items  []archive.MessageRow `json:"items"`
}

type searchListOutput struct {
	Query        string                 `json:"query"`
	Results      []searchResultOutput   `json:"results"`
	TotalMatches int64                  `json:"total_matches"`
	Truncated    bool                   `json:"truncated"`
	WhoResolved  *whoResolvedOutput     `json:"who_resolved,omitempty"`
	Limit        int                    `json:"-"`
	Who          string                 `json:"-"`
	After        string                 `json:"-"`
	Before       string                 `json:"-"`
	TextItems    []archive.SearchResult `json:"-"`
}

type whoResolvedOutput struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type searchResultOutput struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}

type openOutput struct {
	Ref     string              `json:"ref"`
	Chat    openChatOutput      `json:"chat"`
	Message openMessageOutput   `json:"message"`
	Context []openMessageOutput `json:"context"`
}

type openChatOutput struct {
	Name         string   `json:"name"`
	Participants []string `json:"participants,omitempty"`
}

type openMessageOutput struct {
	Ref            string `json:"ref"`
	Time           string `json:"time"`
	Who            string `json:"who"`
	Where          string `json:"where"`
	Text           string `json:"text"`
	FromMe         bool   `json:"from_me"`
	HasAttachments bool   `json:"has_attachments,omitempty"`
	Target         bool   `json:"target,omitempty"`
}

func newListHeader(command string, returned int, total int64, limit int) listHeader {
	return listHeader{
		SchemaVersion: control.StatusSchemaVersion,
		AppID:         "imsgcrawl",
		Command:       command,
		Returned:      returned,
		Total:         total,
		Limit:         limit,
		Complete:      total <= int64(returned),
	}
}
