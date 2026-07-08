package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

const (
	AppID         = "notes"
	DisplayName   = "Notes"
	SchemaVersion = 1
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

type SyncBatch struct {
	Notes        []Note
	Bodies       []BodyInsert
	SyncState    map[string]string
	Coverage     []Coverage
	LastSeenAt   string
	ReplaceNotes bool
}

type SyncStats struct {
	Notes          int
	BodyReads      int
	NewVersions    int
	Observations   int
	WALBytes       int64
	WALCommits     int
	ArchivePath    string
	SourcePath     string
	SyncedAt       string
	CoverageStatus []Coverage
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
	Coverage           []Coverage
	SourceModifiedAt   string
	LastSourcePathHint string
}

type SearchResult struct {
	Ref      string
	Time     string
	Title    string
	Folder   string
	Snippet  string
	NoteID   string
	ShortSHA string
}

type AtTimeResult struct {
	Match         string       `json:"match"`
	RequestedTime string       `json:"requested_time"`
	Note          Note         `json:"note"`
	Version       *VersionBody `json:"version,omitempty"`
	Gap           string       `json:"gap,omitempty"`
}

type Coverage struct {
	SourceClass          string `json:"source_class"`
	Status               string `json:"status"`
	Candidates           int64  `json:"zdata_candidates"`
	AssignedVersions     int64  `json:"assigned_note_versions"`
	UnassignedCandidates int64  `json:"unassigned_candidates"`
	FailureReason        string `json:"failure_reason,omitempty"`
	NextSource           string `json:"next_source,omitempty"`
	InspectedAt          string `json:"inspected_at,omitempty"`
}

type CoverageCount struct {
	Candidates int64
	Assigned   int64
}

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func RefForNote(noteID string) string {
	return AppID + ":note/" + url.PathEscape(strings.TrimSpace(noteID))
}

func RefForVersion(noteID, sha string) string {
	return AppID + ":version/" + url.PathEscape(strings.TrimSpace(noteID)) + "/" + strings.TrimSpace(sha)
}

func NoteIDFromRef(ref string) (string, bool) {
	const prefix = AppID + ":note/"
	value := strings.TrimSpace(ref)
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	id, err := url.PathUnescape(strings.TrimPrefix(value, prefix))
	if err != nil || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}

func VersionFromRef(ref string) (noteID, sha string, ok bool) {
	const prefix = AppID + ":version/"
	value := strings.TrimSpace(ref)
	if !strings.HasPrefix(value, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(value, prefix)
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
