package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

const (
	AppID       = "notes"
	DisplayName = "Notes"
	// SchemaVersion 2 adds the attachments table: attachment files
	// referenced by Apple Notes, copied into the archive at sync time and
	// keyed by attachment UUID.
	SchemaVersion = 2

	NoteRefPrefix    = AppID + ":note/"
	VersionRefPrefix = AppID + ":version/"
)

// Attachment row status values. Every attachment ends up in exactly one of
// these three states -- see AttachmentInsert.
const (
	AttachmentStatusCopied  = "copied"
	AttachmentStatusMissing = "missing"
	AttachmentStatusNoFile  = "no_file"
)

type Note struct {
	ID           string `json:"note_id"`
	Title        string `json:"title,omitempty"`
	Folder       string `json:"folder,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	ModifiedAt   string `json:"modified_at,omitempty"`
	LastSeenAt   string `json:"last_seen_at,omitempty"`
	VersionCount int64  `json:"version_count,omitempty"`
}

type BodyInsert struct {
	NoteID           string
	ZDataSHA256      string
	ZData            []byte
	Source           string
	SourceDetail     string
	SourceSequence   int
	SourceModifiedAt string
	ObservedAt       string
	Title            string
}

type Version struct {
	Ref              string `json:"ref"`
	NoteID           string `json:"note_id"`
	SHA256           string `json:"sha256"`
	ShortSHA         string `json:"short_sha"`
	ZDataBytes       int64  `json:"zdata_bytes"`
	TextStatus       string `json:"text_status"`
	Unsupported      string `json:"unsupported,omitempty"`
	SourceModifiedAt string `json:"source_modified_at,omitempty"`
	FirstObservedAt  string `json:"first_observed_at"`
	LatestObservedAt string `json:"latest_observed_at"`
	Source           string `json:"source,omitempty"`
	SourceDetail     string `json:"source_detail,omitempty"`
	SourceSequence   int    `json:"source_sequence,omitempty"`
}

type VersionBody struct {
	Version
	Title  string `json:"title,omitempty"`
	Folder string `json:"folder,omitempty"`
	Text   string `json:"text,omitempty"`
	ZData  []byte `json:"-"`
}

// AttachmentInsert is one attachment row for the write path. ArchivePath and
// SourceBytes are only meaningful when Status is AttachmentStatusCopied.
type AttachmentInsert struct {
	ID          string
	NoteID      string
	MediaID     string
	Name        string
	Type        string
	ArchivePath string
	Status      string
	SourceBytes int64
}

type SyncBatch struct {
	Notes        []Note
	Bodies       []BodyInsert
	Attachments  []AttachmentInsert
	SyncState    map[string]string
	LastSeenAt   string
	ReplaceNotes bool
}

type SyncStats struct {
	Notes              int
	BodyReads          int
	NewVersions        int
	Observations       int
	AttachmentsCopied  int
	AttachmentsMissing int
	AttachmentsNoFile  int
	WALBytes           int64
	WALCommits         int
	ArchivePath        string
	SourcePath         string
	SyncedAt           string
	// SkippedNoBody counts source notes left out of the archive because they
	// carry no readable body; SkipWarnings names why, one line per reason, so a
	// deficient input is reported rather than archived as an empty row
	// (engineering rules 1.15).
	SkippedNoBody int      `json:"skipped_no_body,omitempty"`
	SkipWarnings  []string `json:"skip_warnings,omitempty"`
}

type Status struct {
	ArchivePath        string
	ArchiveBytes       int64
	SchemaVersion      int
	LastSyncAt         string
	Notes              int64
	Versions           int64
	DecodedVersions    int64
	Observations       int64
	SourceModifiedAt   string
	LastSourcePathHint string
}

type SearchResult struct {
	Ref     string
	Time    string
	Title   string
	Folder  string
	Snippet string
	NoteID  string
}

type AtTimeResult struct {
	Match         string       `json:"match"`
	RequestedTime string       `json:"requested_time"`
	Note          Note         `json:"note"`
	Version       *VersionBody `json:"version,omitempty"`
	Gap           string       `json:"gap,omitempty"`
}

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func RefForNote(noteID string) string {
	return NoteRefPrefix + url.PathEscape(strings.TrimSpace(noteID))
}

func RefForVersion(noteID, sha string) string {
	return VersionRefPrefix + url.PathEscape(strings.TrimSpace(noteID)) + "/" + strings.TrimSpace(sha)
}

func NoteIDFromRef(ref string) (string, bool) {
	value := strings.TrimSpace(ref)
	if !strings.HasPrefix(value, NoteRefPrefix) {
		return "", false
	}
	id, err := url.PathUnescape(strings.TrimPrefix(value, NoteRefPrefix))
	if err != nil || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}

func VersionFromRef(ref string) (noteID, sha string, ok bool) {
	value := strings.TrimSpace(ref)
	if !strings.HasPrefix(value, VersionRefPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(value, VersionRefPrefix)
	before, after, found := strings.Cut(rest, "/")
	if !found {
		return "", "", false
	}
	id, err := url.PathUnescape(before)
	if err != nil || strings.TrimSpace(id) == "" || strings.TrimSpace(after) == "" {
		return "", "", false
	}
	return id, strings.TrimSpace(after), true
}

func ShortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
