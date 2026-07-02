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
	"unicode"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/telecrawl/internal/backup"
	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

type cliError struct {
	code  int
	err   error
	quiet bool
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
	ctx    context.Context
	stdout io.Writer
	stderr io.Writer
	json   bool
	dbPath string
	source string
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
	if rest[0] == "version" {
		if hasHelpFlag(rest[1:]) {
			printCommandUsage(stdout, []string{"version"})
			return nil
		}
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	r := &runtime{ctx: ctx, stdout: stdout, stderr: stderr, json: jsonFlag || *jsonOut, dbPath: *dbPath, source: *source}
	return r.dispatch(rest)
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
	case "open":
		return r.runOpen(args[1:])
	case "backup":
		return r.runBackup(args[1:])
	case "wiretap":
		return r.runImport(args[1:])
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

func (r *runtime) runDoctor(args []string) error {
	fs := flag.NewFlagSet("telecrawl doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", r.source, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	report := telegramdesktop.Probe(r.ctx, telegramdesktop.Options{Path: *path})
	if r.json {
		return r.print(r.doctorEnvelope(report))
	}
	return r.printProbe(report)
}

func (r *runtime) runStatus(args []string) error {
	fs := flag.NewFlagSet("telecrawl status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if r.json {
		return r.print(r.statusEnvelope())
	}
	return r.withStore(func(st *store.Store) error {
		status, err := st.Status(r.ctx)
		if err != nil {
			return err
		}
		return r.print(status)
	})
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
			Progress:                r.stderr,
			ExistingMediaSourcePath: existingMediaSourcePath,
			ExistingMediaRefs:       existingMediaRefs,
		}, st.Path())
		if err != nil {
			return err
		}
		if err := storeImportResult(r.ctx, st, &result, *chat); err != nil {
			return err
		}
		return r.print(result.Stats)
	})
}

func storeImportResult(ctx context.Context, st *store.Store, result *telegramdesktop.ImportResult, chatFilter string) error {
	if err := preserveExistingMediaRefs(ctx, st, result.Stats.SourcePath, result.Messages); err != nil {
		return err
	}
	refreshImportMediaStats(result)
	if strings.TrimSpace(chatFilter) == "" {
		return st.ReplaceAll(ctx, result.Stats, result.Contacts, result.Chats, result.Folders, result.FolderChats, result.Topics, result.Messages)
	}
	if len(result.Chats) == 0 {
		return fmt.Errorf("telegram import returned no chats for --chat %s", chatFilter)
	}
	for _, chat := range result.Chats {
		partial := importResultForChat(*result, chat.JID)
		if err := st.UpsertChat(ctx, partial.Stats, chat.JID, partial.Contacts, partial.Chats, partial.Folders, partial.FolderChats, partial.Topics, partial.Messages); err != nil {
			return err
		}
	}
	return nil
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
	for _, message := range result.Messages {
		if message.ChatJID == chatJID {
			out.Messages = append(out.Messages, message)
		}
	}
	out.Contacts = contactsForMessages(result.Contacts, out.Messages, chatJID)
	return out
}

func contactsForMessages(contacts []store.Contact, messages []store.Message, chatJID string) []store.Contact {
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
		export := control.ContactExport{Contacts: exportContacts(contacts)}
		if err := control.ValidateContactExport(export); err != nil {
			return err
		}
		return r.print(export)
	})
}

func exportContacts(contacts []store.Contact) []control.Contact {
	out := make([]control.Contact, 0, len(contacts))
	byPhone := map[string]store.Contact{}
	phoneOrder := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		if isTelegramServiceContact(contact) {
			continue
		}
		name := contactDisplayName(contact)
		phone := strings.TrimSpace(contact.Phone)
		if name == "" || phone == "" {
			continue
		}
		if current, ok := byPhone[phone]; ok {
			if preferContactExportName(contact, current) {
				byPhone[phone] = contact
			}
		} else {
			byPhone[phone] = contact
			phoneOrder = append(phoneOrder, phone)
		}
	}
	for _, phone := range phoneOrder {
		contact := byPhone[phone]
		name := contactDisplayName(contact)
		out = append(out, control.Contact{DisplayName: name, PhoneNumbers: []string{phone}})
	}
	return out
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
	if name := cleanContactName(contact.FullName, contact); name != "" {
		return name
	}
	return cleanContactName(strings.TrimSpace(contact.FirstName+" "+contact.LastName), contact)
}

func cleanContactName(name string, contact store.Contact) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return ""
	case sameContactText(name, contact.Phone):
		return ""
	case sameContactText(name, contact.JID):
		return ""
	case sameContactText(name, contact.Username):
		return ""
	case sameContactText(name, contact.LID):
		return ""
	case strings.HasPrefix(name, "@"):
		return ""
	case looksLikePhone(name):
		return ""
	default:
		return name
	}
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

func looksLikePhone(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	digits := 0
	other := 0
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digits++
		case strings.ContainsRune(" +()-.", r):
		default:
			other++
		}
	}
	return digits >= 5 && other == 0
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
		messages, err := st.Search(r.ctx, filter)
		if err != nil {
			return err
		}
		total, err := st.CountSearch(r.ctx, filter)
		if err != nil {
			return err
		}
		return r.print(newSearchEnvelope(filter.Query, messages, total))
	})
}

func (r *runtime) runOpen(args []string) error {
	if len(args) != 1 {
		return usageErr(errors.New("open takes exactly one ref"))
	}
	sourcePK, err := parseMessageRef(args[0])
	if err != nil {
		return r.contractError("invalid_ref", "ref is not a telecrawl message ref", "Use a ref returned by telecrawl search --json, such as telecrawl:msg/<id>.")
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
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

func parseMessageRef(ref string) (int64, error) {
	if !strings.HasPrefix(ref, messageRefPrefix) {
		return 0, errors.New("invalid message ref")
	}
	rawID := strings.TrimPrefix(ref, messageRefPrefix)
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
	err := errors.New(message + ". " + remedy)
	if r.json {
		if printErr := r.print(errorEnvelope{Error: body}); printErr != nil {
			return printErr
		}
		return &cliError{code: 1, err: err, quiet: true}
	}
	return &cliError{code: 1, err: err}
}

// messageFilterValueFlags are the filter flags that consume a value, so
// splitFlagArgs can keep flag/value pairs together when flags follow the
// query on the command line.
var messageFilterValueFlags = map[string]bool{
	"chat": true, "sender": true, "topic": true,
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
	if requireQuery {
		if len(positionals) != 1 {
			return filter, usageErr(errors.New("search takes exactly one query"))
		}
		filter.Query = positionals[0]
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

func (r *runtime) printProbe(report telegramdesktop.Report) error {
	if r.json {
		enc := json.NewEncoder(r.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	if _, err := fmt.Fprintf(r.stdout, "path: %s\n", report.Path); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "exists: %t\n", report.Exists); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "accessible: %t\n", report.Accessible); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "store: %s\n", report.Store); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "sqlite_files: %d\n", report.SQLiteFiles); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "tdesktop_files: %d\n", report.TDesktopFiles); err != nil {
		return err
	}
	if report.KeyFiles > 0 {
		if _, err := fmt.Fprintf(r.stdout, "key_files: %d\n", report.KeyFiles); err != nil {
			return err
		}
	}
	if report.PostboxDBs > 0 {
		if _, err := fmt.Fprintf(r.stdout, "postbox_dbs: %d\n", report.PostboxDBs); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(r.stdout, "files_scanned: %d\n", report.FilesScanned); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "bytes_scanned: %d\n", report.BytesScanned); err != nil {
		return err
	}
	if report.AccountDirs > 0 {
		if _, err := fmt.Fprintf(r.stdout, "account_dirs: %d\n", report.AccountDirs); err != nil {
			return err
		}
	}
	if report.Error != "" {
		if _, err := fmt.Fprintf(r.stdout, "error: %s\n", report.Error); err != nil {
			return err
		}
	}
	if report.Note != "" {
		if _, err := fmt.Fprintf(r.stdout, "note: %s\n", report.Note); err != nil {
			return err
		}
	}
	return nil
}

func (r *runtime) print(v any) error {
	enc := json.NewEncoder(r.stdout)
	if r.json {
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	switch value := v.(type) {
	case store.Status:
		if _, err := fmt.Fprintf(r.stdout, "db_path: %s\nchats: %d\nmessages: %d\nunread_chats: %d\nunread_messages: %d\nmedia_messages: %d\n",
			value.DBPath, value.Chats, value.Messages, value.UnreadChats, value.UnreadMessages, value.MediaMessages); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(r.stdout, "folders: %d\ntopics: %d\n", value.Folders, value.Topics); err != nil {
			return err
		}
		if !value.OldestMessage.IsZero() {
			if _, err := fmt.Fprintf(r.stdout, "oldest_message: %s\n", value.OldestMessage.Format(time.RFC3339)); err != nil {
				return err
			}
		}
		if !value.NewestMessage.IsZero() {
			if _, err := fmt.Fprintf(r.stdout, "newest_message: %s\n", value.NewestMessage.Format(time.RFC3339)); err != nil {
				return err
			}
		}
		if !value.LastImportAt.IsZero() {
			if _, err := fmt.Fprintf(r.stdout, "last_import_at: %s\n", value.LastImportAt.Format(time.RFC3339)); err != nil {
				return err
			}
		}
		return nil
	case store.ImportStats:
		if _, err := fmt.Fprintf(r.stdout, "source_path: %s\ndb_path: %s\nchats: %d\nmessages: %d\nmedia_messages: %d\nmedia_files: %d\nmedia_bytes: %d\nstarted_at: %s\nfinished_at: %s\n",
			value.SourcePath, value.DBPath, value.Chats, value.Messages, value.MediaMessages, value.MediaFiles, value.MediaBytes, value.StartedAt.Format(time.RFC3339), value.FinishedAt.Format(time.RFC3339)); err != nil {
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
			if _, err := fmt.Fprintf(r.stdout, "%s\t%s\t%d\t%d\t%s\n", ref, snapshot.Exported.Format(time.RFC3339), snapshot.Counts.Messages, snapshot.Shards, strings.Join(snapshot.Tags, ",")); err != nil {
				return err
			}
		}
		return nil
	case searchEnvelope:
		return r.printSearch(value)
	case openEnvelope:
		return r.printOpen(value)
	default:
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
}

func (r *runtime) printSearch(value searchEnvelope) error {
	for _, item := range value.Results {
		line := item.Time
		if item.Who != "" {
			line += " " + item.Who
		}
		if item.Where != "" {
			line += " in " + item.Where
		}
		if _, err := fmt.Fprintf(r.stdout, "%s\n%s\nref: %s\n\n", line, item.Snippet, item.Ref); err != nil {
			return err
		}
	}
	if value.Truncated {
		_, err := fmt.Fprintf(r.stdout, "showing %d of %d matches; narrow with --limit, --after, --before, or --chat\n", len(value.Results), value.TotalMatches)
		return err
	}
	_, err := fmt.Fprintf(r.stdout, "showing %d of %d matches\n", len(value.Results), value.TotalMatches)
	return err
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
	return &cliError{code: 2, err: err}
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
  telecrawl search "query" [--chat ID] [--topic ID] [--limit N] [--json]
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
		return "usage: telecrawl search \"query\" [--chat ID] [--topic ID] [--sender ID] [--limit N] [--after DATE] [--before DATE] [--from-me|--from-them] [--media] [--pinned] [--json]\n\nSearches archived messages and returns refs for telecrawl open.\n"
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
