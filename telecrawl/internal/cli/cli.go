package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/backup"
	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

type cliError struct {
	code  int
	err   error
	quiet bool
	event string
}

func (e *cliError) Error() string {
	return e.err.Error()
}

func (e *cliError) Unwrap() error {
	return e.err
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 1
	}
	var codeErr *cliError
	if errors.As(err, &codeErr) {
		return codeErr.code
	}
	return 1
}

func ShouldPrintError(err error) bool {
	var codeErr *cliError
	if errors.As(err, &codeErr) {
		return !codeErr.quiet
	}
	return err != nil
}

type runtime struct {
	ctx          context.Context
	stdout       io.Writer
	stderr       io.Writer
	json         bool
	dbPath       string
	source       string
	logStateRoot string
	log          *cklog.Run
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	jsonFlag, args := pullJSONFlag(args)
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return nil
	}
	global := flag.NewFlagSet("telecrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	jsonOut := global.Bool("json", false, "")
	dbPath := global.String("db", defaultDBPath(), "")
	source := global.String("source", "", "")
	versionFlag := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		return usageErr(err)
	}
	if *versionFlag {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	rest := global.Args()
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" || rest[0] == "-h" {
		printUsage(stdout)
		return nil
	}
	r := &runtime{
		ctx:          ctx,
		stdout:       stdout,
		stderr:       stderr,
		json:         jsonFlag || *jsonOut,
		dbPath:       *dbPath,
		source:       *source,
		logStateRoot: logStateRoot(*dbPath),
	}
	run, err := cklog.NewRun(cklog.Options{
		StateRoot: r.logStateRoot,
		CrawlerID: "telecrawl",
		Command:   logCommandName(rest[0]),
		Version:   version,
		Stderr:    stderr,
	})
	if err != nil {
		return err
	}
	r.log = run
	err = r.dispatch(rest)
	if err != nil {
		_ = r.log.Error(errorEventCode(err), err)
	}
	if finishErr := r.log.Finish(err); finishErr != nil {
		return errors.Join(err, finishErr)
	}
	return err
}

func logCommandName(command string) string {
	switch command {
	case "metadata", "import", "sync", "wiretap", "doctor", "status", "chats", "folders", "contacts", "topics", "messages", "search", "who", "open", "backup", "version":
		return command
	default:
		return "unknown"
	}
}

func errorEventCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "command_canceled"
	}
	var codeErr *cliError
	if errors.As(err, &codeErr) && codeErr.event != "" {
		return codeErr.event
	}
	return "command_failed"
}

func pullJSONFlag(args []string) (bool, []string) {
	out := make([]string, 0, len(args))
	jsonOut := false
	for _, arg := range args {
		if arg == "--json" || arg == "-json" {
			jsonOut = true
			continue
		}
		out = append(out, arg)
	}
	return jsonOut, out
}

func (r *runtime) dispatch(args []string) error {
	if len(args) > 1 && hasHelpFlag(args[1:]) {
		printCommandUsage(r.stdout, args)
		return nil
	}
	switch args[0] {
	case "metadata":
		if r.json {
			return r.print(contractMetadata())
		}
		return r.print(controlManifest())
	case "import", "sync":
		return r.runImport(args[1:])
	case "doctor":
		return r.runDoctor(args[1:])
	case "status":
		return r.runStatus(args[1:])
	case "chats":
		return r.runChats(args[1:])
	case "folders":
		return r.runFolders(args[1:])
	case "contacts":
		return r.runContacts(args[1:])
	case "topics":
		return r.runTopics(args[1:])
	case "messages":
		return r.runMessages(args[1:])
	case "search":
		return r.runSearch(args[1:])
	case "who":
		return r.runWho(args[1:])
	case "open":
		return r.runOpen(args[1:])
	case "backup":
		return r.runBackup(args[1:])
	case "wiretap":
		return r.runImport(args[1:])
	case "version":
		_, _ = io.WriteString(r.stdout, version+"\n")
		return nil
	default:
		return usageErr(fmt.Errorf("unknown command %q", args[0]))
	}
}

func (r *runtime) withStore(fn func(*store.Store) error) error {
	st, err := store.Open(r.ctx, r.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (r *runtime) withReadOnlyStore(fn func(*store.Store) error) error {
	st, err := store.OpenReadOnly(r.ctx, r.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (r *runtime) logTail() logTailEnvelope {
	reader, err := cklog.NewReader(r.logStateRoot, "telecrawl")
	if err != nil {
		return logTailEnvelope{}
	}
	lines, err := reader.RecentLines("", 1000)
	if err != nil {
		return logTailEnvelope{}
	}
	currentRunID := ""
	if r.log != nil {
		currentRunID = r.log.RunID()
	}
	var tail logTailEnvelope
	if lastRunID := lastLoggedRunID(lines, currentRunID); lastRunID != "" {
		if summary, ok, err := reader.LastRun(lastRunID); err == nil && ok {
			tail.LastRun = logRunFromSummary(summary)
		}
	}
	if line, ok := mostRecentLoggedError(lines, currentRunID); ok {
		tail.MostRecentError = logErrorFromLine(line)
	}
	return tail
}

func lastLoggedRunID(lines []cklog.Line, skipRunID string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.RunID == "" || line.RunID == "-" || line.RunID == skipRunID || line.Event == "grammar" {
			continue
		}
		return line.RunID
	}
	return ""
}

func mostRecentLoggedError(lines []cklog.Line, skipRunID string) (cklog.Line, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.RunID == skipRunID || line.RunID == "-" {
			continue
		}
		if line.Level == cklog.LevelError {
			return line, true
		}
	}
	return cklog.Line{}, false
}

func logRunFromSummary(summary cklog.RunSummary) *logRunEnvelope {
	out := &logRunEnvelope{
		RunID:     summary.RunID,
		Command:   summary.Command,
		Outcome:   summary.Outcome,
		LastEvent: summary.LastEvent,
		Version:   summary.Version,
	}
	if !summary.StartedAt.IsZero() {
		out.StartedAt = formatLocalTime(summary.StartedAt)
	}
	if !summary.FinishedAt.IsZero() {
		out.FinishedAt = formatLocalTime(summary.FinishedAt)
	}
	return out
}

func logErrorFromLine(line cklog.Line) *logErrorEnvelope {
	return &logErrorEnvelope{
		RunID:   line.RunID,
		Command: line.Command,
		Event:   line.Event,
		Time:    formatLocalTime(line.Timestamp),
		Message: line.Message,
	}
}

func (r *runtime) runDoctor(args []string) error {
	fs := flag.NewFlagSet("telecrawl doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", r.source, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	report := telegramdesktop.Probe(r.ctx, telegramdesktop.Options{Path: *path})
	return r.print(r.doctorEnvelope(report))
}

func (r *runtime) runStatus(args []string) error {
	fs := flag.NewFlagSet("telecrawl status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	return r.print(r.statusEnvelope())
}

func (r *runtime) runImport(args []string) error {
	fs := flag.NewFlagSet("telecrawl import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", r.source, "")
	dialogsLimit := fs.Int("dialogs-limit", 200, "")
	messagesLimit := fs.Int("messages-limit", 500, "")
	chat := fs.String("chat", "", "")
	fetchMedia := fs.Bool("fetch-media", false, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("import takes flags only"))
	}
	progress, stopProgress := r.startCommandProgress("sync_progress", "starting sync")
	defer stopProgress()
	return r.withStore(func(st *store.Store) error {
		var existingMediaSourcePath string
		var existingMediaRefs []telegramdesktop.ExistingMediaRef
		if *fetchMedia {
			var err error
			existingMediaSourcePath, existingMediaRefs, err = existingMediaRefsForImport(r.ctx, st)
			if err != nil {
				return err
			}
		}
		result, err := telegramdesktop.Import(r.ctx, telegramdesktop.ImportOptions{
			Path:                    *path,
			DialogsLimit:            *dialogsLimit,
			MessagesLimit:           *messagesLimit,
			ChatID:                  *chat,
			FetchMedia:              *fetchMedia,
			Progress:                progress,
			ExistingMediaSourcePath: existingMediaSourcePath,
			ExistingMediaRefs:       existingMediaRefs,
		}, st.Path())
		if err != nil {
			return err
		}
		if err := storeImportResult(r.ctx, st, &result, *chat); err != nil {
			return err
		}
		_ = progress.Report(int64(result.Stats.Messages), "sync complete")
		return r.print(result.Stats)
	})
}

type commandProgress struct {
	progress *cklog.Progress
	done     chan struct{}
}

func (r *runtime) startCommandProgress(event, firstMessage string) (*commandProgress, func()) {
	progress := &commandProgress{
		progress: r.log.Progress(cklog.ProgressOptions{Event: event, Unit: "messages"}),
		done:     make(chan struct{}),
	}
	_ = progress.Report(0, firstMessage)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = progress.Report(0, "sync running")
			case <-progress.done:
				return
			case <-r.ctx.Done():
				return
			}
		}
	}()
	return progress, func() {
		close(progress.done)
	}
}

func (p *commandProgress) Report(done int64, message string) error {
	if p == nil || p.progress == nil {
		return nil
	}
	return p.progress.Report(done, message)
}

func storeImportResult(ctx context.Context, st *store.Store, result *telegramdesktop.ImportResult, chatFilter string) error {
	if err := preserveExistingMediaRefs(ctx, st, result.Stats.SourcePath, result.Messages); err != nil {
		return err
	}
	refreshImportMediaStats(result)
	if strings.TrimSpace(chatFilter) == "" {
		if err := st.ReplaceAll(ctx, result.Stats, result.Contacts, result.Chats, result.Folders, result.FolderChats, result.Topics, result.Participants, result.Messages); err != nil {
			return err
		}
		return st.RebuildShortRefs(ctx)
	}
	if len(result.Chats) == 0 {
		return fmt.Errorf("telegram import returned no chats for --chat %s", chatFilter)
	}
	for _, chat := range result.Chats {
		partial := importResultForChat(*result, chat.JID)
		if err := st.UpsertChat(ctx, partial.Stats, chat.JID, partial.Contacts, partial.Chats, partial.Folders, partial.FolderChats, partial.Topics, partial.Participants, partial.Messages); err != nil {
			return err
		}
	}
	return st.RebuildShortRefs(ctx)
}

func refreshImportMediaStats(result *telegramdesktop.ImportResult) {
	result.Stats.MediaMessages = 0
	result.Stats.MediaFiles = 0
	result.Stats.MediaBytes = 0
	mediaFiles := map[string]int64{}
	for _, message := range result.Messages {
		if strings.TrimSpace(message.MediaType) != "" {
			result.Stats.MediaMessages++
		}
		path := strings.TrimSpace(message.MediaPath)
		if path == "" {
			continue
		}
		if _, ok := mediaFiles[path]; !ok {
			mediaFiles[path] = message.MediaSize
		}
	}
	for _, size := range mediaFiles {
		result.Stats.MediaFiles++
		result.Stats.MediaBytes += size
	}
}

func existingMediaRefsForImport(ctx context.Context, st *store.Store) (string, []telegramdesktop.ExistingMediaRef, error) {
	sourcePath, refsByPK, err := existingMediaRefs(ctx, st)
	if err != nil || len(refsByPK) == 0 {
		return sourcePath, nil, err
	}
	refs := make([]telegramdesktop.ExistingMediaRef, 0, len(refsByPK))
	for _, ref := range refsByPK {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].SourcePK < refs[j].SourcePK })
	return sourcePath, refs, nil
}

func preserveExistingMediaRefs(ctx context.Context, st *store.Store, sourcePath string, messages []store.Message) error {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return nil
	}
	existingSourcePath, refs, err := existingMediaRefs(ctx, st)
	if err != nil || existingSourcePath != sourcePath {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	for i := range messages {
		if strings.TrimSpace(messages[i].MediaPath) != "" {
			continue
		}
		ref, ok := refs[messages[i].SourcePK]
		if !ok {
			continue
		}
		if messages[i].MediaType == "" {
			messages[i].MediaType = ref.MediaType
		}
		if messages[i].MediaTitle == "" {
			messages[i].MediaTitle = ref.MediaTitle
		}
		messages[i].MediaPath = ref.MediaPath
		messages[i].MediaSize = ref.MediaSize
	}
	return nil
}

func existingMediaRefs(ctx context.Context, st *store.Store) (string, map[int64]telegramdesktop.ExistingMediaRef, error) {
	status, err := st.Status(ctx)
	if err != nil {
		return "", nil, err
	}
	sourcePath := strings.TrimSpace(status.LastSource)
	if sourcePath == "" {
		return "", nil, nil
	}
	existing, err := st.Messages(ctx, store.MessageFilter{HasMedia: true, Limit: int(^uint(0) >> 1)})
	if err != nil {
		return "", nil, err
	}
	refs := make(map[int64]telegramdesktop.ExistingMediaRef)
	for _, msg := range existing {
		path := strings.TrimSpace(msg.MediaPath)
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		refs[msg.SourcePK] = telegramdesktop.ExistingMediaRef{
			SourcePK:   msg.SourcePK,
			MediaType:  msg.MediaType,
			MediaTitle: msg.MediaTitle,
			MediaPath:  path,
			MediaSize:  msg.MediaSize,
		}
	}
	return sourcePath, refs, nil
}

func importResultForChat(result telegramdesktop.ImportResult, chatJID string) telegramdesktop.ImportResult {
	out := telegramdesktop.ImportResult{Stats: result.Stats, Folders: result.Folders}
	for _, chat := range result.Chats {
		if chat.JID == chatJID {
			out.Chats = append(out.Chats, chat)
		}
	}
	for _, folderChat := range result.FolderChats {
		if folderChat.ChatJID == chatJID {
			out.FolderChats = append(out.FolderChats, folderChat)
		}
	}
	for _, topic := range result.Topics {
		if topic.ChatJID == chatJID {
			out.Topics = append(out.Topics, topic)
		}
	}
	for _, participant := range result.Participants {
		if participant.GroupJID == chatJID {
			out.Participants = append(out.Participants, participant)
		}
	}
	for _, message := range result.Messages {
		if message.ChatJID == chatJID {
			out.Messages = append(out.Messages, message)
		}
	}
	out.Contacts = contactsForMessages(result.Contacts, out.Messages, out.Participants, chatJID)
	return out
}

func contactsForMessages(contacts []store.Contact, messages []store.Message, participants []store.GroupParticipant, chatJID string) []store.Contact {
	peerIDs := map[string]struct{}{}
	if strings.TrimSpace(chatJID) != "" {
		peerIDs[chatJID] = struct{}{}
	}
	for _, message := range messages {
		if strings.TrimSpace(message.ChatJID) != "" {
			peerIDs[message.ChatJID] = struct{}{}
		}
		if strings.TrimSpace(message.SenderJID) != "" {
			peerIDs[message.SenderJID] = struct{}{}
		}
	}
	for _, participant := range participants {
		if strings.TrimSpace(participant.UserJID) != "" {
			peerIDs[participant.UserJID] = struct{}{}
		}
	}
	out := make([]store.Contact, 0, len(peerIDs))
	for _, contact := range contacts {
		if _, ok := peerIDs[contact.JID]; ok {
			out = append(out, contact)
		}
	}
	return out
}

func (r *runtime) runChats(args []string) error {
	fs := flag.NewFlagSet("telecrawl chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	unread := fs.Bool("unread", false, "")
	folder := fs.String("folder", "", "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	return r.withStore(func(st *store.Store) error {
		if *folder != "" {
			chats, err := st.ChatsInFolder(r.ctx, *folder, *limit)
			if err != nil {
				return err
			}
			return r.print(chats)
		}
		chats, err := st.ListChats(r.ctx, *limit, *unread)
		if err != nil {
			return err
		}
		return r.print(chats)
	})
}

func (r *runtime) runFolders(args []string) error {
	fs := flag.NewFlagSet("telecrawl folders", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("folders takes flags only"))
	}
	return r.withStore(func(st *store.Store) error {
		folders, err := st.ListFolders(r.ctx)
		if err != nil {
			return err
		}
		return r.print(folders)
	})
}

func (r *runtime) runContacts(args []string) error {
	if len(args) > 0 && args[0] == "export" {
		return r.runContactsExport(args[1:])
	}
	fs := flag.NewFlagSet("telecrawl contacts", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 100, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("contacts takes flags only"))
	}
	return r.withStore(func(st *store.Store) error {
		contacts, err := st.ListContacts(r.ctx, *limit)
		if err != nil {
			return err
		}
		return r.print(contacts)
	})
}

func (r *runtime) runContactsExport(args []string) error {
	fs := flag.NewFlagSet("telecrawl contacts export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("contacts export takes no arguments"))
	}
	return r.withStore(func(st *store.Store) error {
		contacts, err := st.ExportContacts(r.ctx)
		if err != nil {
			return err
		}
		export := contactExport{Contacts: exportContacts(contacts)}
		return r.print(export)
	})
}

type contactExport struct {
	Contacts []exportContact `json:"contacts"`
}

type exportContact struct {
	DisplayName  string              `json:"display_name"`
	PhoneNumbers []string            `json:"phone_numbers,omitempty"`
	Accounts     map[string][]string `json:"accounts,omitempty"`
}

func exportContacts(contacts []store.Contact) []exportContact {
	out := make([]exportContact, 0, len(contacts))
	byKey := map[string]store.Contact{}
	keys := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		if isTelegramServiceContact(contact) || !isExportablePeerContact(contact) {
			continue
		}
		name := exportContactDisplayName(contact)
		phone := strings.TrimSpace(contact.Phone)
		username := cleanTelegramUsername(contact.Username)
		if name == "" || (phone == "" && username == "") {
			continue
		}
		key := exportContactKey(phone, username)
		if current, ok := byKey[key]; ok {
			if preferContactExportName(contact, current) {
				contact = mergeContactExportIdentifiers(contact, current)
				byKey[key] = contact
			} else {
				byKey[key] = mergeContactExportIdentifiers(current, contact)
			}
		} else {
			byKey[key] = contact
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		contact := byKey[key]
		exported := exportContact{DisplayName: exportContactDisplayName(contact)}
		if phone := strings.TrimSpace(contact.Phone); phone != "" {
			exported.PhoneNumbers = []string{phone}
		}
		if username := cleanTelegramUsername(contact.Username); username != "" {
			exported.Accounts = map[string][]string{"telegram": {username}}
		}
		out = append(out, exported)
	}
	return out
}

func mergeContactExportIdentifiers(base, extra store.Contact) store.Contact {
	if strings.TrimSpace(base.Phone) == "" {
		base.Phone = strings.TrimSpace(extra.Phone)
	}
	if strings.TrimSpace(base.Username) == "" {
		base.Username = strings.TrimSpace(extra.Username)
	}
	return base
}

func isExportablePeerContact(contact store.Contact) bool {
	peerType := strings.TrimSpace(contact.PeerType)
	return peerType == "" || peerType == "user"
}

func exportContactKey(phone, username string) string {
	if phone != "" {
		return "phone:" + phone
	}
	return "telegram:" + strings.ToLower(username)
}

func exportContactDisplayName(contact store.Contact) string {
	if name := contactDisplayName(contact); name != "" {
		return name
	}
	return cleanTelegramUsername(contact.Username)
}

func cleanTelegramUsername(username string) string {
	return strings.TrimSpace(strings.TrimPrefix(username, "@"))
}

func preferContactExportName(candidate, current store.Contact) bool {
	if candidate.UpdatedAt.After(current.UpdatedAt) {
		return true
	}
	if current.UpdatedAt.After(candidate.UpdatedAt) {
		return false
	}
	return len([]rune(contactDisplayName(candidate))) > len([]rune(contactDisplayName(current)))
}

func contactDisplayName(contact store.Contact) string {
	return store.ContactDisplayName(contact)
}

func sameContactText(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a != "" && b != "" && strings.EqualFold(a, b)
}

func isTelegramServiceContact(contact store.Contact) bool {
	return strings.TrimSpace(contact.Phone) == "42777" &&
		sameContactText(contact.FullName, "Telegram") &&
		sameContactText(contact.FirstName, "Telegram") &&
		strings.TrimSpace(contact.LastName) == "" &&
		strings.TrimSpace(contact.Username) == ""
}

func (r *runtime) runTopics(args []string) error {
	fs := flag.NewFlagSet("telecrawl topics", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	chat := fs.String("chat", "", "")
	limit := fs.Int("limit", 100, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("topics takes flags only"))
	}
	return r.withStore(func(st *store.Store) error {
		topics, err := st.ListTopics(r.ctx, *chat, *limit)
		if err != nil {
			return err
		}
		return r.print(topics)
	})
}

func (r *runtime) runMessages(args []string) error {
	filter, err := r.messageFilter("telecrawl messages", args, false, defaultMessageLimit)
	if err != nil {
		return err
	}
	return r.withStore(func(st *store.Store) error {
		messages, err := st.Messages(r.ctx, filter)
		if err != nil {
			return err
		}
		return r.print(messages)
	})
}

func (r *runtime) runSearch(args []string) error {
	filter, err := r.messageFilter("telecrawl search", args, true, defaultSearchLimit)
	if err != nil {
		return err
	}
	if filter.Limit <= 0 {
		filter.Limit = defaultSearchLimit
	}
	if filter.Limit > maxSearchLimit {
		filter.Limit = maxSearchLimit
	}
	return r.withStore(func(st *store.Store) error {
		resolved, err := r.resolveSearchWhoFilter(st, &filter)
		if err != nil {
			return err
		}
		messages, err := st.Search(r.ctx, filter)
		if err != nil {
			return err
		}
		total, err := st.CountSearch(r.ctx, filter)
		if err != nil {
			return err
		}
		shortRefs, err := st.ShortRefsFor(r.ctx, messageRefs(messages))
		if err != nil {
			return err
		}
		return r.print(newSearchEnvelope(filter.Query, messages, total, filter.Who, resolved, shortRefs))
	})
}

func (r *runtime) runWho(args []string) error {
	if len(args) == 0 {
		return usageErr(errors.New("who takes a name"))
	}
	query := normalizeCLIWords(strings.Join(args, " "))
	if query == "" {
		return usageErr(errors.New("who takes a name"))
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		candidates, err := st.ResolveWho(r.ctx, query)
		if err != nil {
			return err
		}
		return r.print(newWhoEnvelope(query, candidates))
	})
}

func messageRefs(messages []store.Message) []string {
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		refs = append(refs, messageRef(message.SourcePK))
	}
	return refs
}

func (r *runtime) resolveSearchWhoFilter(st *store.Store, filter *store.MessageFilter) (*store.WhoCandidate, error) {
	if strings.TrimSpace(filter.Who) == "" {
		return nil, nil
	}
	candidates, err := st.ResolveWho(r.ctx, filter.Who)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, r.unknownWhoError(filter.Who, candidates)
	}
	if len(candidates) > 1 {
		return nil, r.ambiguousWhoError(filter.Query, filter.Who, candidates)
	}
	candidate := candidates[0]
	if candidate.MatchedOnlyByCloseSpelling() {
		return nil, r.unknownWhoError(filter.Who, candidates)
	}
	filter.WhoParticipants = candidate.Participants
	filter.WhoResolved = true
	return &candidate, nil
}

func (r *runtime) runOpen(args []string) error {
	if len(args) != 1 {
		return usageErr(errors.New("open takes exactly one ref"))
	}
	return r.withStore(func(st *store.Store) error {
		sourcePK, err := r.resolveOpenMessageRef(st, args[0])
		if err != nil {
			return err
		}
		window, err := st.OpenMessageWindow(r.ctx, sourcePK, openContextRadius)
		if errors.Is(err, store.ErrMessageNotFound) {
			return r.contractError("not_found", "message was not found in this archive", "Run telecrawl search --json again and use one of the returned refs.")
		}
		if err != nil {
			return err
		}
		return r.print(newOpenEnvelope(window))
	})
}

func (r *runtime) resolveOpenMessageRef(st *store.Store, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		sourcePK, err := parseMessageRef(ref)
		if err != nil {
			return 0, r.contractError("invalid_ref", "ref is not a telecrawl message ref", "Use a ref returned by telecrawl search --json, such as telecrawl:msg/<id>.")
		}
		return sourcePK, nil
	}
	fullRefs, err := st.ResolveShortRef(r.ctx, ref)
	if errors.Is(err, store.ErrUnknownShortRef) {
		return 0, r.contractError("unknown_short_ref", "short ref was not found in this archive", "Run telecrawl search and copy the displayed short ref, or use a full ref from telecrawl search --json.")
	}
	if errors.Is(err, store.ErrAmbiguousShortRef) {
		return 0, r.contractError("ambiguous_short_ref", "short ref matches more than one archived message", "Run telecrawl search again and use the longer displayed ref or the full ref from telecrawl search --json.")
	}
	if err != nil {
		return 0, err
	}
	if len(fullRefs) != 1 {
		return 0, r.contractError("unknown_short_ref", "short ref was not found in this archive", "Run telecrawl search and copy the displayed short ref, or use a full ref from telecrawl search --json.")
	}
	sourcePK, err := parseMessageRef(fullRefs[0])
	if err != nil {
		return 0, err
	}
	return sourcePK, nil
}

func parseMessageRef(ref string) (int64, error) {
	if !strings.HasPrefix(ref, store.MessageRefPrefix) {
		return 0, errors.New("invalid message ref")
	}
	rawID := strings.TrimPrefix(ref, store.MessageRefPrefix)
	if rawID == "" {
		return 0, errors.New("invalid message ref")
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != rawID {
		return 0, errors.New("invalid message ref")
	}
	return id, nil
}

func (r *runtime) contractError(code, message, remedy string) error {
	body := contractErrorBody{Code: code, Message: message, Remedy: remedy}
	err := newRemediedError(message, remedy)
	if r.json {
		if printErr := r.print(errorEnvelope{Error: body}); printErr != nil {
			return printErr
		}
		return &cliError{code: 1, err: err, quiet: true, event: code}
	}
	return &cliError{code: 1, err: err, event: code}
}

func (r *runtime) ambiguousWhoError(query, who string, candidates []store.WhoCandidate) error {
	body := contractErrorBody{
		Code:       "ambiguous_who",
		Message:    "--who matched more than one person",
		Remedy:     "Retry with one identifier from candidates.",
		Candidates: whoCandidates(candidates),
	}
	return r.contractBodyError(4, body, ambiguousWhoText(query, who, candidates))
}

func (r *runtime) unknownWhoError(who string, didYouMean []store.WhoCandidate) error {
	candidates := whoCandidates(didYouMean)
	body := contractErrorBody{
		Code:    "unknown_who",
		Message: "--who did not match a person",
		Remedy:  "Run telecrawl who <name>, or search without --who to check whether matching messages exist.",
		Hint:    "Search without --who to check whether matching messages exist.",
	}
	body.DidYouMean = &candidates
	return r.contractBodyError(5, body, unknownWhoText(who, didYouMean))
}

func (r *runtime) contractBodyError(code int, body contractErrorBody, human string) error {
	if r.json {
		if printErr := r.print(errorEnvelope{Error: body}); printErr != nil {
			return printErr
		}
		return &cliError{code: code, err: errors.New(body.Message), quiet: true, event: body.Code}
	}
	return &cliError{code: code, err: errors.New(human), event: body.Code}
}

func ambiguousWhoText(query, who string, candidates []store.WhoCandidate) string {
	var out strings.Builder
	fmt.Fprintf(&out, "ambiguous --who %q: %d people match.\n\n", who, len(candidates))
	writeWhoTable(&out, candidates, terminalWidth())
	if retry := retrySearchExample(query, candidates); retry != "" {
		fmt.Fprintf(&out, "\nRetry with: %s", retry)
	}
	return strings.TrimRight(out.String(), "\n")
}

func unknownWhoText(who string, didYouMean []store.WhoCandidate) string {
	var out strings.Builder
	fmt.Fprintf(&out, "unknown --who %q: no person matched.", who)
	if len(didYouMean) == 0 {
		out.WriteString("\nSearch without --who to check whether matching messages exist.")
		return out.String()
	}
	out.WriteString("\n\nDid you mean:\n")
	writeWhoTable(&out, didYouMean, terminalWidth())
	if retry := retrySearchExample("", didYouMean); retry != "" {
		fmt.Fprintf(&out, "\nRetry with: %s", retry)
	}
	return strings.TrimRight(out.String(), "\n")
}

func retrySearchExample(query string, candidates []store.WhoCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	who := firstRetryIdentifier(candidates[0])
	if who == "" {
		return ""
	}
	parts := []string{"telecrawl", "search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteShellArg(query))
	}
	parts = append(parts, "--who", quoteShellArg(who))
	return strings.Join(parts, " ")
}

func firstRetryIdentifier(candidate store.WhoCandidate) string {
	for _, identifier := range candidate.Identifiers {
		if strings.TrimSpace(identifier) != "" {
			return identifier
		}
	}
	return candidate.Who
}

func quoteShellArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\"'") {
		return strconv.Quote(value)
	}
	return value
}

type remediedError struct {
	message string
	remedy  string
}

func newRemediedError(message, remedy string) error {
	return remediedError{message: strings.TrimSpace(message), remedy: strings.TrimSpace(remedy)}
}

func (e remediedError) Error() string {
	switch {
	case e.message != "" && e.remedy != "":
		return e.message + ". " + e.remedy
	case e.message != "":
		return e.message
	default:
		return e.remedy
	}
}

func (e remediedError) Unwrap() error {
	return cklog.WorldMustChange{Err: errors.New(e.message), Message: e.message, Remedy: e.remedy}
}

// messageFilterValueFlags are the filter flags that consume a value, so
// splitFlagArgs can keep flag/value pairs together when flags follow the
// query on the command line.
var messageFilterValueFlags = map[string]bool{
	"chat": true, "sender": true, "topic": true, "who": true,
	"limit": true, "after": true, "before": true,
}

// splitFlagArgs lets flags appear after positional arguments, which Go's
// flag package otherwise stops parsing at.
func splitFlagArgs(args []string) (flags, positionals []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if !strings.Contains(name, "=") && messageFilterValueFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return flags, positionals
}

func (r *runtime) messageFilter(name string, args []string, requireQuery bool, defaultLimit int) (store.MessageFilter, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var filter store.MessageFilter
	fs.StringVar(&filter.ChatJID, "chat", "", "")
	fs.StringVar(&filter.Sender, "sender", "", "")
	fs.StringVar(&filter.TopicID, "topic", "", "")
	if requireQuery {
		fs.StringVar(&filter.Who, "who", "", "")
	}
	fs.IntVar(&filter.Limit, "limit", defaultLimit, "")
	after := fs.String("after", "", "")
	before := fs.String("before", "", "")
	fromMe := fs.Bool("from-me", false, "")
	fromThem := fs.Bool("from-them", false, "")
	fs.BoolVar(&filter.HasMedia, "media", false, "")
	fs.BoolVar(&filter.Pinned, "pinned", false, "")
	fs.BoolVar(&filter.Asc, "asc", false, "")
	flagTokens, positionals := splitFlagArgs(args)
	if err := fs.Parse(flagTokens); err != nil {
		return filter, usageErr(err)
	}
	whoProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "who" {
			whoProvided = true
		}
	})
	if requireQuery {
		if whoProvided {
			filter.Who = normalizeCLIWords(filter.Who)
			if filter.Who == "" {
				return filter, usageErr(errors.New("--who requires an identity"))
			}
		}
		filterOnly := whoProvided || strings.TrimSpace(*after) != "" || strings.TrimSpace(*before) != ""
		switch {
		case len(positionals) == 0 && !filterOnly:
			return filter, usageErr(errors.New("search takes a query unless --who, --after, or --before is set\n\n" + commandUsage([]string{"search"})))
		case len(positionals) > 1:
			return filter, usageErr(errors.New("search takes at most one query"))
		case len(positionals) == 1:
			filter.Query = positionals[0]
		}
	} else if len(positionals) != 0 {
		return filter, usageErr(errors.New("messages takes flags only"))
	}
	if *after != "" {
		t, err := parseDate(*after)
		if err != nil {
			return filter, usageErr(err)
		}
		filter.After = &t
	}
	if *before != "" {
		t, err := parseDate(*before)
		if err != nil {
			return filter, usageErr(err)
		}
		filter.Before = &t
	}
	if *fromMe && *fromThem {
		return filter, usageErr(errors.New("--from-me and --from-them conflict"))
	}
	if *fromMe || *fromThem {
		v := *fromMe
		filter.FromMe = &v
	}
	return filter, nil
}

func normalizeCLIWords(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (r *runtime) runBackup(args []string) error {
	if len(args) == 0 {
		return usageErr(errors.New("backup needs subcommand: init, push, pull, status, snapshots"))
	}
	switch args[0] {
	case "init":
		return r.backupInit(args[1:])
	case "push":
		return r.backupPush(args[1:])
	case "pull":
		return r.backupPull(args[1:])
	case "status":
		return r.backupStatus(args[1:])
	case "snapshots":
		return r.backupSnapshots(args[1:])
	default:
		return usageErr(fmt.Errorf("unknown backup command %q", args[0]))
	}
}

func backupFlags(name string) (*flag.FlagSet, *backup.Options, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &backup.Options{}
	fs.StringVar(&opts.ConfigPath, "config", backup.DefaultConfigPath(), "")
	fs.StringVar(&opts.Repo, "repo", "", "")
	fs.StringVar(&opts.Remote, "remote", "", "")
	fs.StringVar(&opts.Identity, "identity", "", "")
	fs.StringVar(&opts.Ref, "ref", "", "")
	fs.StringVar(&opts.Tag, "tag", "", "")
	fs.IntVar(&opts.Limit, "limit", 20, "")
	fs.Func("recipient", "", func(value string) error {
		opts.Recipients = append(opts.Recipients, value)
		return nil
	})
	noPush := fs.Bool("no-push", false, "")
	return fs, opts, noPush
}

func (r *runtime) backupInit(args []string) error {
	fs, opts, noPush := backupFlags("telecrawl backup init")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	opts.Push = !*noPush
	cfg, recipient, err := backup.Init(r.ctx, *opts)
	if err != nil {
		return err
	}
	return r.print(map[string]any{"repo": cfg.Repo, "remote": cfg.Remote, "identity": cfg.Identity, "recipient": recipient})
}

func (r *runtime) backupPush(args []string) error {
	fs, opts, noPush := backupFlags("telecrawl backup push")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	opts.Push = !*noPush
	return r.withStore(func(st *store.Store) error {
		result, err := backup.Push(r.ctx, st, *opts)
		if err != nil {
			return err
		}
		return r.print(result)
	})
}

func (r *runtime) backupPull(args []string) error {
	fs, opts, _ := backupFlags("telecrawl backup pull")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	return r.withStore(func(st *store.Store) error {
		result, err := backup.Pull(r.ctx, st, *opts)
		if err != nil {
			return err
		}
		return r.print(result)
	})
}

func (r *runtime) backupStatus(args []string) error {
	fs, opts, _ := backupFlags("telecrawl backup status")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	manifest, repo, err := backup.Status(r.ctx, *opts)
	if err != nil {
		return err
	}
	return r.print(map[string]any{"repo": repo, "manifest": manifest})
}

func (r *runtime) backupSnapshots(args []string) error {
	fs, opts, _ := backupFlags("telecrawl backup snapshots")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("backup snapshots takes flags only"))
	}
	if opts.Limit < 1 {
		return usageErr(errors.New("backup snapshots --limit must be greater than zero"))
	}
	snapshots, repo, err := backup.Snapshots(r.ctx, *opts)
	if err != nil {
		return err
	}
	if r.json {
		return r.print(map[string]any{"repo": repo, "snapshots": snapshots})
	}
	return r.print(snapshots)
}

func (r *runtime) print(v any) error {
	enc := json.NewEncoder(r.stdout)
	if r.json {
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	switch value := v.(type) {
	case statusEnvelope:
		return r.printStatus(value)
	case doctorOutput:
		return r.printDoctor(value)
	case store.ImportStats:
		if _, err := fmt.Fprintf(r.stdout, "source_path: %s\ndb_path: %s\nchats: %d\nmessages: %d\nmedia_messages: %d\nmedia_files: %d\nmedia_bytes: %d\nstarted_at: %s\nfinished_at: %s\n",
			value.SourcePath, value.DBPath, value.Chats, value.Messages, value.MediaMessages, value.MediaFiles, value.MediaBytes, formatLocalTime(value.StartedAt), formatLocalTime(value.FinishedAt)); err != nil {
			return err
		}
		if hasRemoteMediaStats(value) {
			if _, err := fmt.Fprintf(
				r.stdout,
				"remote_media_candidates: %d\nremote_media_attempted: %d\nremote_media_downloads: %d\nremote_media_missing: %d\nremote_media_unavailable: %d\nremote_media_timeouts: %d\nremote_media_errors: %d\n",
				value.RemoteMediaCandidates,
				value.RemoteMediaAttempted,
				value.RemoteMediaDownloads,
				value.RemoteMediaMissing,
				value.RemoteMediaUnavailable,
				value.RemoteMediaTimeouts,
				value.RemoteMediaErrors,
			); err != nil {
				return err
			}
		}
		return nil
	case []backup.Snapshot:
		for _, snapshot := range value {
			ref := snapshot.Ref
			if len(snapshot.Tags) > 0 {
				ref = snapshot.Tags[0]
			}
			if _, err := fmt.Fprintf(r.stdout, "%s\t%s\t%d\t%d\t%s\n", ref, formatLocalTime(snapshot.Exported), snapshot.Counts.Messages, snapshot.Shards, strings.Join(snapshot.Tags, ",")); err != nil {
				return err
			}
		}
		return nil
	case searchEnvelope:
		return r.printSearch(value)
	case whoEnvelope:
		return r.printWho(value)
	case openEnvelope:
		return r.printOpen(value)
	default:
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
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
		Log:       renderLogTail(value.Log),
	})
}

func statusRenderFields(counts []countEnvelope) []render.Field {
	fields := make([]render.Field, 0, len(counts))
	for _, count := range counts {
		label := statusCountLabel(count.ID, count.Label)
		display := strconv.FormatInt(count.Value, 10)
		if count.ID == "since" && count.Value == 0 {
			display = "not available"
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
	return &render.Freshness{LastSync: freshness.LastSync}
}

func statusCountLabel(id, fallback string) string {
	switch id {
	case "messages":
		return "Messages"
	case "chats":
		return "Chats"
	case "since":
		return "Since"
	default:
		return humanLabel(fallback)
	}
}

func (r *runtime) printDoctor(value doctorOutput) error {
	return render.WriteDoctor(r.stdout, doctorRenderChecks(value.Checks), renderLogTail(value.Log))
}

func doctorRenderChecks(checks []doctorCheck) []render.Check {
	out := make([]render.Check, 0, len(checks))
	for _, check := range checks {
		name := strings.TrimSpace(check.ID)
		if name == "" {
			name = strings.TrimSpace(check.Label)
		}
		out = append(out, render.Check{
			Name:    name,
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return out
}

func humanLabel(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func renderLogTail(value logTailEnvelope) render.LogTail {
	var tail render.LogTail
	if value.LastRun != nil {
		tail.LastRun = &cklog.RunSummary{
			RunID:      value.LastRun.RunID,
			Command:    value.LastRun.Command,
			Outcome:    value.LastRun.Outcome,
			StartedAt:  parseRenderTime(value.LastRun.StartedAt),
			FinishedAt: parseRenderTime(value.LastRun.FinishedAt),
			LastEvent:  value.LastRun.LastEvent,
			Version:    value.LastRun.Version,
		}
	}
	if value.MostRecentError != nil {
		tail.MostRecentError = &cklog.Line{
			RunID:     value.MostRecentError.RunID,
			Command:   value.MostRecentError.Command,
			Event:     value.MostRecentError.Event,
			Timestamp: parseRenderTime(value.MostRecentError.Time),
			Message:   value.MostRecentError.Message,
		}
	}
	return tail
}

func parseRenderTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func (r *runtime) printSearch(value searchEnvelope) error {
	if value.WhoResolved != nil {
		query := normalizeCLIWords(value.WhoQuery)
		if query == "" {
			query = value.WhoResolved.Who
		}
		if _, err := fmt.Fprintf(r.stdout, "%s \u2192 %s\n", query, value.WhoResolved.Who); err != nil {
			return err
		}
		if len(value.Results) > 0 {
			if _, err := io.WriteString(r.stdout, "\n"); err != nil {
				return err
			}
		}
	}
	for _, item := range value.Results {
		line := item.Time
		if item.Who != "" {
			line += " " + item.Who
		}
		if item.Where != "" {
			line += " in " + item.Where
		}
		ref := item.Ref
		if item.ShortRef != "" {
			ref = item.ShortRef + " (" + item.Ref + ")"
		}
		if _, err := fmt.Fprintf(r.stdout, "%s\n%s\nref: %s\n\n", line, item.Snippet, ref); err != nil {
			return err
		}
	}
	if value.Truncated {
		_, err := fmt.Fprintf(r.stdout, "showing %d of %d matches; narrow with --limit, --after, --before, --chat, or --who\n", len(value.Results), value.TotalMatches)
		return err
	}
	_, err := fmt.Fprintf(r.stdout, "showing %d of %d matches\n", len(value.Results), value.TotalMatches)
	return err
}

func (r *runtime) printWho(value whoEnvelope) error {
	candidates := make([]store.WhoCandidate, 0, len(value.Candidates))
	for _, candidate := range value.Candidates {
		candidates = append(candidates, store.WhoCandidate{
			Who:         candidate.Who,
			Identifiers: candidate.Identifiers,
			LastSeen:    parseRenderTime(candidate.LastSeen),
			Messages:    candidate.Messages,
		})
	}
	writeWhoTable(r.stdout, candidates, terminalWidth())
	return nil
}

func writeWhoTable(w io.Writer, candidates []store.WhoCandidate, width int) {
	rows := make([][]string, 0, len(candidates)+1)
	rows = append(rows, []string{"Who", "Last seen", "Messages", "Identifiers"})
	for _, candidate := range candidates {
		rows = append(rows, []string{
			outputField(candidate.Who),
			formatOptionalTime(candidate.LastSeen),
			strconv.Itoa(candidate.Messages),
			strings.Join(candidate.Identifiers, ", "),
		})
	}
	writeFittedTable(w, rows, width)
}

func writeFittedTable(w io.Writer, rows [][]string, width int) {
	if width <= 0 {
		width = 100
	}
	if len(rows) == 0 {
		return
	}
	colWidths := whoTableColumnWidths(rows, width)
	for _, row := range rows {
		wrapped := wrapTableRow(row, colWidths)
		lineCount := 0
		if len(wrapped) > 0 {
			lineCount = len(wrapped[0])
		}
		for line := 0; line < lineCount; line++ {
			for col := 0; col < len(colWidths); col++ {
				value := ""
				if line < len(wrapped[col]) {
					value = wrapped[col][line]
				}
				if col == len(colWidths)-1 {
					_, _ = io.WriteString(w, value)
					continue
				}
				_, _ = fmt.Fprintf(w, "%-*s  ", colWidths[col], value)
			}
			_, _ = io.WriteString(w, "\n")
		}
	}
}

func whoTableColumnWidths(rows [][]string, width int) []int {
	messagesWidth := max(tableColumnWidth(rows, 2), len("Messages"))
	lastSeenWidth := max(tableColumnWidth(rows, 1), len("Last seen"))
	lastSeenWidth = min(max(lastSeenWidth, len(time.RFC3339)), 25)
	if width < 60 {
		lastSeenWidth = 10
	}
	whoWidth := min(max(tableColumnWidth(rows, 0), len("Who")), 30)
	identWidth := width - whoWidth - lastSeenWidth - messagesWidth - 6
	if identWidth < 8 {
		identWidth = 8
		whoWidth = max(8, width-lastSeenWidth-messagesWidth-identWidth-6)
	}
	return []int{whoWidth, lastSeenWidth, messagesWidth, identWidth}
}

func tableColumnWidth(rows [][]string, col int) int {
	width := 0
	for _, row := range rows {
		if col >= len(row) {
			continue
		}
		width = max(width, len([]rune(row[col])))
	}
	return width
}

func wrapTableRow(row []string, widths []int) [][]string {
	wrapped := make([][]string, len(widths))
	maxLines := 1
	for i, width := range widths {
		value := ""
		if i < len(row) {
			value = row[i]
		}
		wrapped[i] = wrapTableCell(value, width)
		if len(wrapped[i]) > maxLines {
			maxLines = len(wrapped[i])
		}
	}
	for i := range wrapped {
		for len(wrapped[i]) < maxLines {
			wrapped[i] = append(wrapped[i], "")
		}
	}
	return wrapped
}

func wrapTableCell(value string, width int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{""}
	}
	if width <= 0 {
		return []string{value}
	}
	var lines []string
	for _, field := range strings.Fields(value) {
		for len([]rune(field)) > width {
			lines = append(lines, string([]rune(field)[:width]))
			field = string([]rune(field)[width:])
		}
		if len(lines) == 0 || len([]rune(lines[len(lines)-1]))+1+len([]rune(field)) > width {
			lines = append(lines, field)
			continue
		}
		lines[len(lines)-1] += " " + field
	}
	return lines
}

func terminalWidth() int {
	width, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS")))
	if err == nil && width >= 40 {
		return width
	}
	return 100
}

func (r *runtime) printOpen(value openEnvelope) error {
	if _, err := fmt.Fprintf(r.stdout, "chat: %s (%s)\n", value.Chat.Name, value.Chat.Ref); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "ref: %s\n", value.Ref); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "target: %s %s\n", value.Message.Time, value.Message.Sender.DisplayName); err != nil {
		return err
	}
	if strings.TrimSpace(value.Message.Text) != "" {
		if _, err := fmt.Fprintf(r.stdout, "text: %s\n", value.Message.Text); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(r.stdout, "context: %d before, %d after", value.ContextWindow.Before, value.ContextWindow.After); err != nil {
		return err
	}
	if value.ContextWindow.BeforeTruncated || value.ContextWindow.AfterTruncated {
		if _, err := io.WriteString(r.stdout, " (bounded; more messages omitted)"); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(r.stdout, "\n"); err != nil {
		return err
	}
	for _, message := range value.Context {
		marker := " "
		if message.IsTarget {
			marker = ">"
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			text = mediaSummary(message)
		}
		if _, err := fmt.Fprintf(r.stdout, "%s %s  %s: %s\n", marker, message.Time, message.Sender.DisplayName, text); err != nil {
			return err
		}
	}
	return nil
}

func mediaSummary(message openMessage) string {
	switch {
	case message.MediaTitle != "":
		return "[" + message.MediaTitle + "]"
	case message.MediaType != "":
		return "[" + message.MediaType + "]"
	default:
		return "[empty message]"
	}
}

func hasRemoteMediaStats(stats store.ImportStats) bool {
	return stats.RemoteMediaCandidates != 0 ||
		stats.RemoteMediaAttempted != 0 ||
		stats.RemoteMediaDownloads != 0 ||
		stats.RemoteMediaMissing != 0 ||
		stats.RemoteMediaUnavailable != 0 ||
		stats.RemoteMediaTimeouts != 0 ||
		stats.RemoteMediaErrors != 0
}

func usageErr(err error) error {
	return &cliError{code: 2, err: err, event: "usage_error"}
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		switch arg {
		case "--help", "-help", "-h":
			return true
		}
	}
	return false
}

func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, `telecrawl: Telegram archive probe/import CLI

usage:
  telecrawl doctor [--path PATH] [--json]
  telecrawl metadata [--json]
  telecrawl import [--path PATH] [--chat ID] [--dialogs-limit N] [--messages-limit N] [--fetch-media] [--json]
  telecrawl status [--json]
  telecrawl folders [--json]
  telecrawl contacts [--limit N] [--json]
  telecrawl contacts export [--json]
  telecrawl chats [--limit N] [--unread] [--folder ID] [--json]
  telecrawl topics --chat ID [--limit N] [--json]
  telecrawl messages [--chat ID] [--topic ID] [--limit N] [--after DATE] [--json]
  telecrawl search ["query"] [--who PERSON] [--chat ID] [--topic ID] [--limit N] [--json]
  telecrawl who NAME [--json]
  telecrawl open telecrawl:msg/ID [--json]
  telecrawl backup init|push|pull|status|snapshots [--json]
  telecrawl version

notes:
  import auto-detects Telegram Desktop tdata or native macOS Postbox data
  import archives local cached Postbox media by default; --fetch-media also tries Telegram cloud media
  backup writes encrypted age shards to a git repo
`)
}

func printCommandUsage(w io.Writer, args []string) {
	_, _ = io.WriteString(w, commandUsage(args))
}

func commandUsage(args []string) string {
	if len(args) == 0 {
		return topUsageText()
	}
	switch args[0] {
	case "doctor":
		return "usage: telecrawl doctor [--path PATH] [--json]\n\nChecks Telegram source access and archive readability.\n"
	case "metadata":
		return "usage: telecrawl metadata [--json]\n\nPrints the crawler manifest and contract capabilities.\n"
	case "import", "sync", "wiretap":
		return "usage: telecrawl import [--path PATH] [--chat ID] [--dialogs-limit N] [--messages-limit N] [--fetch-media] [--json]\n\nImports Telegram data into the local archive. This mutates the archive.\n"
	case "status":
		return "usage: telecrawl status [--json]\n\nReads archive status without importing or syncing.\n"
	case "folders":
		return "usage: telecrawl folders [--json]\n\nLists Telegram folders from the local archive.\n"
	case "contacts":
		if len(args) > 1 && args[1] == "export" {
			return "usage: telecrawl contacts export [--json]\n\nExports safe contact records from archived conversation evidence.\n"
		}
		return "usage: telecrawl contacts [--limit N] [--json]\n\nLists contacts from the local archive.\n"
	case "chats":
		return "usage: telecrawl chats [--limit N] [--unread] [--folder ID] [--json]\n\nLists chats from the local archive.\n"
	case "topics":
		return "usage: telecrawl topics --chat ID [--limit N] [--json]\n\nLists forum topics for one archived chat.\n"
	case "messages":
		return "usage: telecrawl messages [--chat ID] [--topic ID] [--sender ID] [--limit N] [--after DATE] [--before DATE] [--from-me|--from-them] [--media] [--pinned] [--asc] [--json]\n\nLists a bounded set of archived messages.\n"
	case "search":
		return "usage: telecrawl search [\"query\"] [--who PERSON] [--chat ID] [--topic ID] [--sender ID] [--limit N] [--after DATE] [--before DATE] [--from-me|--from-them] [--media] [--pinned] [--json]\n\nSearches archived messages and returns refs for telecrawl open. Query is optional when --who, --after, or --before is set.\n"
	case "who":
		return "usage: telecrawl who NAME [--json]\n\nResolves a name or identifier to archived Telegram participants.\n"
	case "open":
		return "usage: telecrawl open telecrawl:msg/ID [--json]\n\nOpens one search ref with a bounded same-chat context window.\n"
	case "backup":
		if len(args) > 1 {
			return backupCommandUsage(args[1])
		}
		return "usage: telecrawl backup init|push|pull|status|snapshots [--json]\n\nManages encrypted archive backups.\n"
	case "version":
		return "usage: telecrawl version\n\nPrints the telecrawl version.\n"
	default:
		return topUsageText()
	}
}

func backupCommandUsage(command string) string {
	switch command {
	case "init":
		return "usage: telecrawl backup init [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--recipient AGE-RECIPIENT] [--no-push] [--json]\n\nInitialises encrypted archive backup settings.\n"
	case "push":
		return "usage: telecrawl backup push [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--recipient AGE-RECIPIENT] [--tag TAG] [--no-push] [--json]\n\nExports and pushes an encrypted archive backup.\n"
	case "pull":
		return "usage: telecrawl backup pull [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--ref REF] [--json]\n\nRestores the archive from an encrypted backup.\n"
	case "status":
		return "usage: telecrawl backup status [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--json]\n\nReads backup repository status.\n"
	case "snapshots":
		return "usage: telecrawl backup snapshots [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--limit N] [--json]\n\nLists encrypted backup snapshots.\n"
	default:
		return "usage: telecrawl backup init|push|pull|status|snapshots [--json]\n\nManages encrypted archive backups.\n"
	}
}

func topUsageText() string {
	var out strings.Builder
	printUsage(&out)
	return out.String()
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "telecrawl.db"
	}
	return filepath.Join(home, ".telecrawl", "telecrawl.db")
}

func logStateRoot(dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return defaultBaseDir()
	}
	dir := filepath.Dir(dbPath)
	if dir == "." || dir == "" {
		return defaultBaseDir()
	}
	return dir
}

func defaultLogDir() string {
	return filepath.Join(logStateRoot(defaultDBPath()), "telecrawl", "logs")
}

func parseDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid date %q", value)
}

func defaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".telecrawl"
	}
	return filepath.Join(home, ".telecrawl")
}
