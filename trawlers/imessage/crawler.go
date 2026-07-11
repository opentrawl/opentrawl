package imsgcrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

const (
	appID      = "imessage"
	display    = "iMessage"
	staleAfter = 7 * 24 * time.Hour
)

type Crawler struct {
	messages messagesOptions
}

var (
	_ trawlkit.Crawler         = (*Crawler)(nil)
	_ trawlkit.Syncer          = (*Crawler)(nil)
	_ trawlkit.Searcher        = (*Crawler)(nil)
	_ trawlkit.WhoMatcher      = (*Crawler)(nil)
	_ trawlkit.ChatLister      = (*Crawler)(nil)
	_ trawlkit.Opener          = (*Crawler)(nil)
	_ trawlkit.ContactExporter = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) Info() trawlkit.Info {
	return trawlkit.Info{
		ID:          appID,
		Surface:     "imessage",
		DisplayName: display,
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"apple-messages", "sqlite", "contact-handles", "message-archive", "message-text-search"},
		},
	}
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus(appID, "Archive has not been synced.")
	status.State = "missing"
	status.SetupRequirements = []control.SetupRequirement{imessageSetupRequirement(ctx)}
	if req.Store == nil {
		return &status, nil
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be read."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	archiveStatus, err := st.Status(ctx)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be inspected."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	status.DatabasePath = archiveStatus.ArchivePath
	status.DatabaseBytes = archiveStatus.ArchiveBytes
	status.LastSyncAt = localRFC3339(archiveStatus.LastSyncAt)
	status.Counts = statusCounts(archiveStatus)
	switch {
	case archiveStatus.Messages == 0:
		status.State = "empty"
		status.Summary = "Archive is empty."
	case isStale(archiveStatus.LastSyncAt):
		status.State = "stale"
		status.Summary = "Archive is stale."
	default:
		status.State = "ok"
		status.Summary = "Recently synced."
	}
	return &status, nil
}

func imessageSetupRequirement(ctx context.Context) control.SetupRequirement {
	_, err := messages.Status(ctx, messages.DefaultChatDBPath())
	return imessageSetupRequirementForError(err)
}

func imessageSetupRequirementForError(err error) control.SetupRequirement {
	state := control.SetupStateForError(err)
	return control.NewSetupRequirement(
		"full_disk_access",
		control.SetupKindFullDiskAccess,
		state,
		"OpenTrawl reads the local Messages database.",
		control.SetupActionOpenFullDiskAccess,
		nil,
	)
}

func (c *Crawler) Doctor(ctx context.Context, req *trawlkit.Request) (*trawlkit.Doctor, error) {
	return &trawlkit.Doctor{Checks: []trawlkit.Check{
		checkSourceStore(ctx),
		checkArchive(ctx, req),
		checkFullDiskAccess(),
	}}, nil
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.SearchResult{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	options := archive.SearchOptions{
		Limit:     query.Limit,
		After:     appleDateOrZero(query.After),
		HasAfter:  !query.After.IsZero(),
		Before:    appleDateOrZero(query.Before),
		HasBefore: !query.Before.IsZero(),
	}
	if strings.TrimSpace(query.Who) != "" {
		candidate, err := resolveArchiveWho(ctx, st, query.Who)
		if err != nil {
			return trawlkit.SearchResult{}, err
		}
		options.Who = &candidate
	}
	page, err := st.SearchPage(ctx, query.Text, options)
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	hits := make([]trawlkit.Hit, 0, len(page.Items))
	for _, item := range page.Items {
		hit, err := searchHit(item)
		if err != nil {
			return trawlkit.SearchResult{}, err
		}
		hits = append(hits, hit)
	}
	return trawlkit.SearchResult{
		Results:      hits,
		TotalMatches: int(page.Total),
		Truncated:    page.Total > int64(len(hits)),
	}, nil
}

func (c *Crawler) Who(ctx context.Context, req *trawlkit.Request, person string) ([]whomatch.Candidate, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolution, err := st.ResolveWho(ctx, person)
	if err != nil {
		return nil, err
	}
	out := make([]whomatch.Candidate, 0, len(resolution.Candidates))
	for _, candidate := range resolution.Candidates {
		out = append(out, whoCandidate(candidate))
	}
	return out, nil
}

func (c *Crawler) ContactExport(ctx context.Context, req *trawlkit.Request) (*control.ContactExport, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	contacts, err := st.ExportContacts(ctx)
	if err != nil {
		return nil, err
	}
	return &control.ContactExport{Contacts: contacts}, nil
}

func statusCounts(status archive.Status) []control.Count {
	since := int64(0)
	if status.EarliestMessageDate > 0 {
		since = int64(archive.AppleDateTime(status.EarliestMessageDate).Year())
	}
	return []control.Count{
		control.NewCount("messages", "messages", status.Messages),
		control.NewCount("chats", "chats", status.Chats),
		control.NewCount("named_contacts", "named contacts", status.NamedContacts),
		control.NewCount("since", "since", since),
	}
}

func checkSourceStore(ctx context.Context) trawlkit.Check {
	if _, err := messages.Status(ctx, messages.DefaultChatDBPath()); err != nil {
		return trawlkit.Check{
			ID:      "source_store",
			State:   "fail",
			Message: "cannot read the source database",
			Remedy:  fullDiskAccessRemedy,
		}
	}
	return trawlkit.Check{ID: "source_store", State: "ok"}
}

func checkArchive(ctx context.Context, req *trawlkit.Request) trawlkit.Check {
	if req.Store == nil {
		return trawlkit.Check{
			ID:      "archive",
			State:   "fail",
			Message: "the archive database has not been synced",
			Remedy:  "run trawl imessage sync",
		}
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.Check{
			ID:      "archive",
			State:   "fail",
			Message: "cannot read the archive database",
			Remedy:  "run trawl imessage sync to rebuild the archive",
		}
	}
	if _, err := st.Status(ctx); err != nil {
		return trawlkit.Check{
			ID:      "archive",
			State:   "fail",
			Message: "cannot inspect the archive database",
			Remedy:  "run trawl imessage sync to rebuild the archive",
		}
	}
	if _, err := st.Chats(ctx, archive.ChatListOptions{Limit: 1}); errors.Is(err, archive.ErrSchemaOutdated) {
		return trawlkit.Check{
			ID:      "archive",
			State:   "fail",
			Message: "archive schema predates this version",
			Remedy:  "run trawl imessage sync to upgrade the archive schema",
		}
	}
	return trawlkit.Check{ID: "archive", State: "ok"}
}

func checkFullDiskAccess() trawlkit.Check {
	if err := canReadDirectory(filepath.Dir(messages.DefaultChatDBPath())); err != nil {
		return trawlkit.Check{
			ID:      "full_disk_access",
			State:   "fail",
			Message: "cannot read the Messages directory",
			Remedy:  fullDiskAccessRemedy,
		}
	}
	return trawlkit.Check{ID: "full_disk_access", State: "ok"}
}

func canReadDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func resolveArchiveWho(ctx context.Context, st *archive.Store, who string) (archive.WhoCandidate, error) {
	resolution, err := st.ResolveWho(ctx, who)
	if err != nil {
		return archive.WhoCandidate{}, err
	}
	candidate, ok := resolution.FilterCandidate()
	if !ok {
		return archive.WhoCandidate{}, fmt.Errorf("resolved who %q was not unique", who)
	}
	return candidate, nil
}

func whoCandidate(candidate archive.WhoCandidate) whomatch.Candidate {
	return whomatch.Candidate{
		Who:         strings.Join(strings.Fields(candidate.Who), " "),
		Identifiers: append([]string{}, candidate.Identifiers...),
		LastSeen:    parseArchiveTime(candidate.LastSeen),
		Messages:    candidate.Messages,
	}
}

func searchHit(item archive.SearchResult) (trawlkit.Hit, error) {
	t := parseArchiveTime(item.Time)
	if t.IsZero() && strings.TrimSpace(item.Time) != "" {
		return trawlkit.Hit{}, fmt.Errorf("parse message time %q", item.Time)
	}
	return trawlkit.Hit{
		Ref:      archive.MessageRef(item.MessageID),
		ShortRef: item.ShortRef,
		Time:     t,
		Who:      outputField(senderName(item.FromMe, item.SenderLabel)),
		Where:    outputField(searchChatDisplayName(item)),
		Snippet:  outputField(searchSnippet(item)),
	}, nil
}

func appleDateOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return archive.AppleDateFromTime(t)
}

func localRFC3339(value string) string {
	if value == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return t.Local().Format(time.RFC3339)
}

func isStale(value string) bool {
	lastSync, err := time.Parse(time.RFC3339Nano, value)
	return err != nil || time.Since(lastSync) > staleAfter
}
