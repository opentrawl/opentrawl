package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/addressbook"
	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

// Sync-state lives in the one trawlkit state.Store. Scalar sync
// markers are keyed under the "sync" entity type; derived-state bookkeeping
// under "derived".
const (
	syncSource            = "imessage"
	syncEntityType        = "sync"
	stateLastSyncAt       = "last_sync_at"
	stateSourcePath       = "source_path"
	stateSourceBytes      = "source_bytes"
	stateSourceModifiedAt = "source_modified_at"
)

type Store struct {
	store *store.Store
	path  string
	owned bool
}

type SyncOptions struct {
	ArchivePath           string
	SourcePath            string
	AddressBookPaths      []string
	UseDefaultAddressBook bool
}

// DefaultPaths is the one archive path layout, from trawlkit/config. The base
// dir is the fleet-wide state root, ~/.opentrawl/imessage.
func DefaultPaths() config.Paths {
	paths, _ := config.App{Name: "imessage", BaseDir: "~/.opentrawl/imessage"}.DefaultPaths()
	return paths
}

func DefaultPath() string {
	return DefaultPaths().DBPath
}

func Exists(path string) bool {
	if path == "" {
		path = DefaultPath()
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	st, err := store.Open(ctx, store.Options{Path: path, Schema: schema + state.Schema + shortref.Schema})
	if err != nil {
		return nil, err
	}
	return &Store{store: st, path: path, owned: true}, nil
}

// ErrArchiveSync marks failures after source extraction and contact reads,
// when sync is opening or writing the archive.
var ErrArchiveSync = errors.New("archive sync failed")

type archiveSyncError struct {
	err error
}

func (e archiveSyncError) Error() string {
	return e.err.Error()
}

func (e archiveSyncError) Unwrap() error {
	return e.err
}

func (e archiveSyncError) Is(target error) bool {
	return target == ErrArchiveSync
}

func archiveSyncErr(err error) error {
	if err == nil {
		return nil
	}
	return archiveSyncError{err: err}
}

func Use(ctx context.Context, st *store.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	if _, err := st.DB().ExecContext(ctx, schema+state.Schema+shortref.Schema); err != nil {
		return nil, fmt.Errorf("apply current iMessage archive schema: %w", err)
	}
	return &Store{store: st, path: path}, nil
}

func UseExisting(ctx context.Context, st *store.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	return &Store{store: st, path: path}, nil
}

func (s *Store) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	if !s.owned {
		return nil
	}
	return s.store.Close()
}

func Sync(ctx context.Context, archivePath, sourcePath string) (SyncResult, error) {
	options := SyncOptions{ArchivePath: archivePath, SourcePath: sourcePath}
	if strings.TrimSpace(sourcePath) == "" || filepath.Clean(sourcePath) == filepath.Clean(messages.DefaultChatDBPath()) {
		options.UseDefaultAddressBook = true
	}
	return SyncWithOptions(ctx, options)
}

func SyncWithOptions(ctx context.Context, options SyncOptions) (SyncResult, error) {
	return syncWithStore(ctx, nil, options)
}

func SyncInto(ctx context.Context, opened *store.Store, options SyncOptions) (SyncResult, error) {
	return syncWithStore(ctx, opened, options)
}

func syncWithStore(ctx context.Context, opened *store.Store, options SyncOptions) (SyncResult, error) {
	totalStarted := time.Now()
	extractStarted := time.Now()
	data, err := messages.ExtractArchive(ctx, options.SourcePath)
	extractElapsed := time.Since(extractStarted)
	if err != nil {
		return SyncResult{}, err
	}
	contactsStarted := time.Now()
	contactNames, err := syncContactNames(ctx, options)
	contactsElapsed := time.Since(contactsStarted)
	if err != nil {
		return SyncResult{}, err
	}
	mapStarted := time.Now()
	contactMappings := applyContactNames(&data, contactNames)
	ownerHandles := applyOwnerHandles(&data, contactNames, contactMappings)
	mapElapsed := time.Since(mapStarted)
	var st *Store
	if opened != nil {
		st, err = Use(ctx, opened, options.ArchivePath)
	} else {
		st, err = Open(ctx, options.ArchivePath)
	}
	if err != nil {
		return SyncResult{}, archiveSyncErr(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	writeStarted := time.Now()
	if err := st.ReplaceAll(ctx, data, contactMappings, ownerHandles, now); err != nil {
		return SyncResult{}, archiveSyncErr(err)
	}
	writeElapsed := time.Since(writeStarted)
	return SyncResult{
		ArchivePath:      st.path,
		SourcePath:       data.SourcePath,
		SourceBytes:      data.SourceBytes,
		SourceModifiedAt: data.SourceModifiedAt.Format(time.RFC3339),
		SyncedAt:         now.Format(time.RFC3339),
		Handles:          len(data.Handles),
		NamedContacts:    len(contactMappings),
		Chats:            len(data.Chats),
		Participants:     len(data.Participants),
		ChatMessages:     len(data.ChatMessages),
		Messages:         len(data.Messages),
		ExtractElapsed:   extractElapsed,
		ContactsElapsed:  contactsElapsed,
		MapElapsed:       mapElapsed,
		WriteElapsed:     writeElapsed,
		TotalElapsed:     time.Since(totalStarted),
	}, nil
}

func (s *Store) ReplaceAll(ctx context.Context, data messages.ArchiveData, contactMappings []ContactMapping, ownerHandles []OwnerHandle, syncedAt time.Time) error {
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"messages_fts", "messages", "chat_messages", "chat_participants", "chats", "handles", "contact_mappings", "owner_handles", "sync_state"} {
			if _, err := tx.ExecContext(ctx, "delete from "+table); err != nil {
				return err
			}
		}
		for _, h := range data.Handles {
			if _, err := tx.ExecContext(ctx, insertHandlesSQL, h.SourceRowID, h.ID, h.Service, h.UncanonicalizedID, h.DisplayName); err != nil {
				return err
			}
		}
		for _, mapping := range contactMappings {
			if _, err := tx.ExecContext(ctx, insertContactMappingSQL, mapping.Kind, mapping.NormalizedHandle, mapping.ContactKey, mapping.DisplayName); err != nil {
				return err
			}
		}
		for _, c := range data.Chats {
			_, err := tx.ExecContext(ctx, insertChatsSQL,
				c.SourceRowID, c.GUID, c.ChatIdentifier, c.ServiceName, c.DisplayName, c.RoomName, boolInt(c.IsArchived))
			if err != nil {
				return err
			}
		}
		for _, p := range data.Participants {
			if _, err := tx.ExecContext(ctx, insertChatParticipantsSQL, p.ChatRowID, p.HandleRowID); err != nil {
				return err
			}
		}
		for _, cm := range data.ChatMessages {
			if _, err := tx.ExecContext(ctx, insertChatMessagesSQL, cm.ChatRowID, cm.MessageRowID); err != nil {
				return err
			}
		}
		for _, m := range data.Messages {
			_, err := tx.ExecContext(ctx, insertMessagesSQL,
				m.SourceRowID,
				m.GUID,
				m.HandleRowID,
				m.Date,
				m.Service,
				m.Account,
				boolInt(m.IsFromMe),
				m.Text,
				boolInt(m.HasAttachments),
				boolInt(m.IsRead),
				m.IsForward,
				m.ItemType,
				m.GroupActionType,
				m.MessageActionType,
				m.AssociatedMessageType,
			)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, insertMessagesFTSSQL, m.SourceRowID, m.Text); err != nil {
				return err
			}
		}
		for _, h := range ownerHandles {
			if _, err := tx.ExecContext(ctx, `insert or ignore into owner_handles(kind, normalized_handle) values(?, ?)`, h.Kind, h.NormalizedHandle); err != nil {
				return err
			}
		}
		if err := replaceSyncState(ctx, tx, data, syncedAt); err != nil {
			return err
		}
		shortReferenceAssignmentCandidatesForRecordsPublishedByIMessageTransaction := make(
			[]trawlkit.ShortReferenceAssignmentCandidate,
			0,
			len(data.Messages)+len(data.Chats),
		)
		for _, message := range data.Messages {
			shortReferenceAssignmentCandidatesForRecordsPublishedByIMessageTransaction = append(
				shortReferenceAssignmentCandidatesForRecordsPublishedByIMessageTransaction,
				trawlkit.ShortReferenceAssignmentCandidate{
					StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(
						MessageRef(strconv.FormatInt(message.SourceRowID, 10)),
					),
				},
			)
		}
		for _, chat := range data.Chats {
			shortReferenceAssignmentCandidatesForRecordsPublishedByIMessageTransaction = append(
				shortReferenceAssignmentCandidatesForRecordsPublishedByIMessageTransaction,
				trawlkit.ShortReferenceAssignmentCandidate{
					StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(
						ChatRef(strconv.FormatInt(chat.SourceRowID, 10)),
					),
				},
			)
		}
		return trawlkit.ReplaceShortReferencesForCompleteArchiveRecordSnapshotUsingCallerOwnedSQLTransaction(
			ctx,
			tx,
			shortReferenceAssignmentCandidatesForRecordsPublishedByIMessageTransaction,
		)
	})
}

func syncContactNames(ctx context.Context, options SyncOptions) ([]addressbook.ContactName, error) {
	if options.AddressBookPaths != nil {
		return addressbook.Extract(ctx, options.AddressBookPaths)
	}
	if options.UseDefaultAddressBook {
		return addressbook.ExtractDefault(ctx)
	}
	return nil, nil
}

func applyContactNames(data *messages.ArchiveData, names []addressbook.ContactName) []ContactMapping {
	if len(names) == 0 {
		return nil
	}
	lookup := addressbook.NewLookup(names)
	seen := map[string]ContactMapping{}
	for i := range data.Handles {
		name, ok := lookup.Match(data.Handles[i].ID)
		if !ok {
			continue
		}
		data.Handles[i].DisplayName = name.DisplayName
		key := name.Kind + ":" + name.Handle
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = ContactMapping{
			Kind:             name.Kind,
			NormalizedHandle: name.Handle,
			ContactKey:       name.ContactKey,
			DisplayName:      name.DisplayName,
		}
	}
	out := make([]ContactMapping, 0, len(seen))
	for _, mapping := range seen {
		out = append(out, mapping)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].NormalizedHandle < out[j].NormalizedHandle
	})
	return out
}

func replaceSyncState(ctx context.Context, tx *sql.Tx, data messages.ArchiveData, syncedAt time.Time) error {
	syncState := state.New(tx)
	entries := []struct{ id, value string }{
		{stateLastSyncAt, syncedAt.UTC().Format(time.RFC3339)},
		{stateSourcePath, data.SourcePath},
		{stateSourceBytes, strconv.FormatInt(data.SourceBytes, 10)},
		{stateSourceModifiedAt, data.SourceModifiedAt.UTC().Format(time.RFC3339)},
	}
	for _, entry := range entries {
		if err := syncState.Set(ctx, syncSource, syncEntityType, entry.id, entry.value); err != nil {
			return err
		}
	}
	return nil
}
