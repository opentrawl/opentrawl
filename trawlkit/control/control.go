package control

import (
	"errors"
	"os"
	"strings"
	"time"
)

const (
	RunnerManifestVersion = 2
	SchemaVersion         = RunnerManifestVersion
	ContractVersion       = 1
	StatusSchemaVersion   = "trawlkit.control.v1"
)

type Manifest struct {
	SchemaVersion   int                `json:"schema_version"`
	ContractVersion int                `json:"contract_version"`
	ID              string             `json:"id"`
	DisplayName     string             `json:"display_name"`
	Version         string             `json:"version"`
	Aliases         []string           `json:"aliases,omitempty"`
	Binary          Binary             `json:"binary"`
	Branding        Branding           `json:"branding"`
	Paths           Paths              `json:"paths"`
	Commands        map[string]Command `json:"commands"`
	Headlines       []string           `json:"headlines,omitempty"`
	Capabilities    []string           `json:"capabilities,omitempty"`
	Privacy         Privacy            `json:"privacy"`
}

type Binary struct {
	Name string `json:"name"`
}

type Branding struct {
	SymbolName       string `json:"symbol_name,omitempty"`
	AccentColor      string `json:"accent_color,omitempty"`
	IconPath         string `json:"icon_path,omitempty"`
	BundleIdentifier string `json:"bundle_identifier,omitempty"`
}

type Paths struct {
	DefaultConfig   string `json:"default_config,omitempty"`
	ConfigEnv       string `json:"config_env,omitempty"`
	DefaultDatabase string `json:"default_database,omitempty"`
	DefaultCache    string `json:"default_cache,omitempty"`
	DefaultLogs     string `json:"default_logs,omitempty"`
	DefaultShare    string `json:"default_share,omitempty"`
}

type Command struct {
	Title      string   `json:"title,omitempty"`
	Argv       []string `json:"argv"`
	JSON       bool     `json:"json"`
	Mutates    bool     `json:"mutates"`
	Store      string   `json:"store,omitempty"`
	Legacy     bool     `json:"legacy,omitempty"`
	Deprecated bool     `json:"deprecated,omitempty"`
	Headline   bool     `json:"headline,omitempty"`
	// Secondary marks a specialist verb the namespace listing moves under a
	// "More verbs" heading, out of the primary namespace list.
	Secondary bool   `json:"secondary,omitempty"`
	Flags     []Flag `json:"flags,omitempty"`
}

type Flag struct {
	Name    string `json:"name"`
	Usage   string `json:"usage,omitempty"`
	Default string `json:"default,omitempty"`
}

type Privacy struct {
	ContainsPrivateMessages bool     `json:"contains_private_messages"`
	ExportsSecrets          bool     `json:"exports_secrets"`
	LocalOnlyScopes         []string `json:"local_only_scopes,omitempty"`
}

type Status struct {
	SchemaVersion     string             `json:"schema_version"`
	AppID             string             `json:"app_id"`
	GeneratedAt       string             `json:"generated_at"`
	State             string             `json:"state"`
	Summary           string             `json:"summary"`
	ConfigPath        string             `json:"config_path,omitempty"`
	DatabasePath      string             `json:"database_path,omitempty"`
	DatabaseBytes     int64              `json:"database_bytes,omitempty"`
	WALBytes          int64              `json:"wal_bytes,omitempty"`
	LastSyncAt        string             `json:"last_sync_at,omitempty"`
	LastImportAt      string             `json:"last_import_at,omitempty"`
	LastExportAt      string             `json:"last_export_at,omitempty"`
	Counts            []Count            `json:"counts,omitempty"`
	Freshness         *Freshness         `json:"freshness,omitempty"`
	Share             *Share             `json:"share,omitempty"`
	Remote            *Remote            `json:"remote,omitempty"`
	Databases         []Database         `json:"databases,omitempty"`
	SetupRequirements []SetupRequirement `json:"setup_requirements,omitempty"`
	Warnings          []string           `json:"warnings,omitempty"`
	Errors            []string           `json:"errors,omitempty"`
}

// SetupKind identifies the prerequisite a source needs before it can sync.
// The values are part of the app and crawler contract; keep them stable.
type SetupKind string

const (
	SetupKindFullDiskAccess   SetupKind = "full_disk_access"
	SetupKindPhotosPermission SetupKind = "photos_permission"
	SetupKindAccount          SetupKind = "account"
	SetupKindPairing          SetupKind = "pairing"
	SetupKindArchiveImport    SetupKind = "archive_import"
)

// SetupState describes the current state of one independent prerequisite.
type SetupState string

const (
	SetupStateReady       SetupState = "ready"
	SetupStateNeedsAction SetupState = "needs_action"
	SetupStateUnavailable SetupState = "unavailable"
)

// SetupActionKind identifies the supported remedy for a setup requirement.
type SetupActionKind string

const (
	SetupActionNone               SetupActionKind = "none"
	SetupActionOpenFullDiskAccess SetupActionKind = "open_full_disk_access"
	SetupActionRequestPhotos      SetupActionKind = "request_photos"
	SetupActionRunCommand         SetupActionKind = "run_command"
	SetupActionChooseArchive      SetupActionKind = "choose_archive"
)

// SetupRequirement is source-owned typed setup information. Command is an
// argument vector, never a shell string.
type SetupRequirement struct {
	ID          string          `json:"id"`
	Kind        SetupKind       `json:"kind"`
	State       SetupState      `json:"state"`
	Explanation string          `json:"explanation"`
	Action      SetupActionKind `json:"action"`
	Command     []string        `json:"command,omitempty"`
}

func NewSetupRequirement(id string, kind SetupKind, state SetupState, explanation string, action SetupActionKind, command []string) SetupRequirement {
	if state == SetupStateReady || state == SetupStateUnavailable {
		action = SetupActionNone
		command = nil
	}
	return SetupRequirement{
		ID:          strings.TrimSpace(id),
		Kind:        kind,
		State:       state,
		Explanation: strings.TrimSpace(explanation),
		Action:      action,
		Command:     append([]string(nil), command...),
	}
}

// SetupStateForError maps a source read failure to the setup state the app
// can show without parsing an error sentence.
func SetupStateForError(err error) SetupState {
	if err == nil {
		return SetupStateReady
	}
	if errors.Is(err, os.ErrPermission) {
		return SetupStateNeedsAction
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "permission denied") ||
		strings.Contains(message, "operation not permitted") ||
		strings.Contains(message, "not authorised") ||
		strings.Contains(message, "not authorized") ||
		strings.Contains(message, "authorization denied") {
		return SetupStateNeedsAction
	}
	return SetupStateUnavailable
}

type Count struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value int64  `json:"value"`
}

type Freshness struct {
	Status            string `json:"status"`
	AgeSeconds        int64  `json:"age_seconds,omitempty"`
	StaleAfterSeconds int64  `json:"stale_after_seconds,omitempty"`
}

type Share struct {
	Enabled     bool   `json:"enabled"`
	RepoPath    string `json:"repo_path,omitempty"`
	Remote      string `json:"remote,omitempty"`
	Branch      string `json:"branch,omitempty"`
	NeedsUpdate bool   `json:"needs_update,omitempty"`
}

type Remote struct {
	Enabled      bool   `json:"enabled"`
	Mode         string `json:"mode,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
	Archive      string `json:"archive,omitempty"`
	LastIngestAt string `json:"last_ingest_at,omitempty"`
	LastSyncAt   string `json:"last_sync_at,omitempty"`
	NeedsUpdate  bool   `json:"needs_update,omitempty"`
}

type Database struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	Kind       string  `json:"kind"`
	Role       string  `json:"role"`
	Path       string  `json:"path"`
	Endpoint   string  `json:"endpoint,omitempty"`
	Archive    string  `json:"archive,omitempty"`
	IsPrimary  bool    `json:"is_primary"`
	Bytes      int64   `json:"bytes"`
	ModifiedAt string  `json:"modified_at,omitempty"`
	Counts     []Count `json:"counts,omitempty"`
}

func NewManifest(id, displayName, binaryName string) Manifest {
	return Manifest{
		SchemaVersion:   RunnerManifestVersion,
		ContractVersion: ContractVersion,
		ID:              strings.TrimSpace(id),
		DisplayName:     strings.TrimSpace(displayName),
		Binary:          Binary{Name: strings.TrimSpace(binaryName)},
		Commands:        map[string]Command{},
	}
}

func NewStatus(appID, summary string) Status {
	return Status{
		SchemaVersion: StatusSchemaVersion,
		AppID:         strings.TrimSpace(appID),
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		State:         "unknown",
		Summary:       strings.TrimSpace(summary),
	}
}

func NewCount(id, label string, value int64) Count {
	return Count{ID: strings.TrimSpace(id), Label: strings.TrimSpace(label), Value: value}
}

func SQLiteDatabase(id, label, role, path string, primary bool, counts []Count) Database {
	db := Database{
		ID:        strings.TrimSpace(id),
		Label:     strings.TrimSpace(label),
		Kind:      "sqlite",
		Role:      strings.TrimSpace(role),
		Path:      strings.TrimSpace(path),
		IsPrimary: primary,
		Counts:    append([]Count(nil), counts...),
	}
	if db.Role == "" {
		db.Role = "archive"
	}
	if info, err := os.Stat(db.Path); err == nil {
		db.Bytes = info.Size()
		db.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
	}
	return db
}

func RemoteDatabase(id, label, role, kind, endpoint, archive string, primary bool, counts []Count) Database {
	db := Database{
		ID:        strings.TrimSpace(id),
		Label:     strings.TrimSpace(label),
		Kind:      strings.TrimSpace(kind),
		Role:      strings.TrimSpace(role),
		Endpoint:  strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		Archive:   strings.TrimSpace(archive),
		IsPrimary: primary,
		Counts:    append([]Count(nil), counts...),
	}
	if db.Kind == "" {
		db.Kind = "remote"
	}
	if db.Role == "" {
		db.Role = "archive"
	}
	return db
}
