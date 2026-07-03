package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/openclaw/crawlkit/control"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/wacrawl/internal/backup"
	"github.com/openclaw/wacrawl/internal/store"
	"github.com/openclaw/wacrawl/internal/whatsappdb"
)

const (
	defaultMessageLimit = 20
	maxMessageLimit     = 200
	messageRefPrefix    = store.MessageRefPrefix
	openWindowEachSide  = 10
)

type cliError struct {
	code int
	err  error
}

func (e *cliError) Error() string { return e.err.Error() }

func (e *cliError) Unwrap() error { return e.err }

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	return 1
}

type app struct {
	stdout io.Writer
	stderr io.Writer
	json   bool
	dbPath string
	source string
	runLog *cklog.Run
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	args, jsonAnywhere := extractJSONFlag(args)
	global := flag.NewFlagSet("wacrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	jsonOut := global.Bool("json", false, "")
	dbPath := global.String("db", defaultDBPath(), "")
	source := global.String("source", "", "")
	versionFlag := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return nil
		}
		return usageErr(err)
	}
	if *versionFlag {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	a := &app{stdout: stdout, stderr: stderr, json: *jsonOut || jsonAnywhere, dbPath: *dbPath, source: *source}
	rest := global.Args()
	if len(rest) == 0 {
		printUsage(stdout)
		return nil
	}
	return a.runCommand(ctx, rest)
}

func (a *app) runCommand(ctx context.Context, rest []string) error {
	run, err := a.newLogRun(logCommandName(rest))
	if err != nil {
		return err
	}
	a.runLog = run
	err = a.dispatch(ctx, rest)
	if err != nil {
		_ = run.Error(errorEvent(rest, err), err)
	}
	if finishErr := run.Finish(err); err == nil {
		return finishErr
	}
	return err
}

func (a *app) dispatch(ctx context.Context, rest []string) error {
	if rest[0] == "help" {
		if len(rest) == 1 {
			printUsage(a.stdout)
			return nil
		}
		if printCommandUsage(a.stdout, rest[1:]...) {
			return nil
		}
		return usageErr(fmt.Errorf("unknown help topic %q", strings.Join(rest[1:], " ")))
	}
	switch rest[0] {
	case "metadata":
		return a.print(controlManifest())
	case "doctor":
		return a.runDoctor(ctx, rest[1:])
	case "import", "sync":
		return a.runImport(ctx, rest[0], rest[1:])
	case "status":
		return a.runStatus(ctx, rest[1:])
	case "chats":
		return a.runChats(ctx, rest[1:])
	case "contacts":
		return a.runContacts(ctx, rest[1:])
	case "unread":
		return a.runUnread(ctx, rest[1:])
	case "messages":
		return a.runMessages(ctx, rest[1:])
	case "search":
		return a.runSearch(ctx, rest[1:])
	case "open":
		return a.runOpen(ctx, rest[1:])
	case "sql":
		return a.runSQL(ctx, rest[1:])
	case "web":
		return a.runWeb(ctx, rest[1:])
	case "backup":
		return a.runBackup(ctx, rest[1:])
	default:
		return usageErr(fmt.Errorf("unknown command %q", rest[0]))
	}
}

func extractJSONFlag(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	jsonOut := false
	literalArgs := false
	for _, arg := range args {
		if literalArgs {
			out = append(out, arg)
			continue
		}
		if arg == "--" {
			literalArgs = true
			out = append(out, arg)
			continue
		}
		if arg == "--json" {
			jsonOut = true
			continue
		}
		out = append(out, arg)
	}
	return out, jsonOut
}

func (a *app) newLogRun(command string) (*cklog.Run, error) {
	return cklog.NewRun(cklog.Options{
		StateRoot:    logStateRoot(a.dbPath),
		CrawlerID:    "wacrawl",
		Command:      command,
		Version:      version,
		Stderr:       a.stderr,
		JSONProgress: a.json,
	})
}

func logStateRoot(dbPath string) string {
	if strings.TrimSpace(dbPath) == "" {
		return filepath.Dir(defaultDBPath())
	}
	dir := filepath.Dir(strings.TrimSpace(dbPath))
	if dir == "" {
		return "."
	}
	return dir
}

func logCommandName(rest []string) string {
	if len(rest) == 0 {
		return "command"
	}
	name := strings.ToLower(strings.TrimSpace(rest[0]))
	var out strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '_' || r == '-' || r == '.':
			out.WriteRune(r)
		default:
			out.WriteRune('_')
		}
	}
	if out.Len() == 0 {
		return "command"
	}
	return out.String()
}

func errorEvent(rest []string, err error) string {
	var contract *contractFailure
	if errors.As(err, &contract) && contract.Code != "" {
		return logEventName(contract.Code)
	}
	var ce *cliError
	if errors.As(err, &ce) && ce.code == 2 {
		return "usage_error"
	}
	if errors.Is(err, errNoArchive) {
		return "archive_missing"
	}
	return logEventName(logCommandName(rest) + "_failed")
}

func logEventName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case (r >= '0' && r <= '9') || r == '_':
			if i > 0 {
				out.WriteRune(r)
			}
		default:
			if i > 0 {
				out.WriteRune('_')
			}
		}
	}
	name := strings.Trim(out.String(), "_")
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return "run_failed"
	}
	return name
}

func worldMustChange(err error, remedy string) error {
	return cklog.WorldMustChange{Err: err, Message: err.Error(), Remedy: remedy}
}

func (a *app) importProgress(command string) (func(whatsappdb.ImportProgress), func()) {
	if a.runLog == nil {
		return func(whatsappdb.ImportProgress) {}, func() {}
	}
	progress := a.runLog.Progress(cklog.ProgressOptions{Event: logEventName(command + "_progress"), Unit: "stage", Total: 5})
	var (
		mu   sync.Mutex
		last = whatsappdb.ImportProgress{Total: 5, Message: "starting sync"}
	)
	report := func(event whatsappdb.ImportProgress) {
		if event.Total <= 0 {
			event.Total = 5
		}
		if strings.TrimSpace(event.Message) == "" {
			event.Message = "syncing"
		}
		mu.Lock()
		last = event
		mu.Unlock()
		_ = progress.Report(event.Done, event.Message)
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				event := last
				mu.Unlock()
				if strings.TrimSpace(event.Message) != "" {
					_ = progress.Report(event.Done, event.Message)
				}
			case <-done:
				return
			}
		}
	}()
	stop := func() {
		close(done)
		<-stopped
	}
	return report, stop
}

func (a *app) logTail() logTailEnvelope {
	reader, err := cklog.NewReader(logStateRoot(a.dbPath), "wacrawl")
	if err != nil {
		return logTailEnvelope{}
	}
	lines, err := reader.RecentLines("", 1000)
	if err != nil {
		return logTailEnvelope{}
	}
	currentRunID := ""
	if a.runLog != nil {
		currentRunID = a.runLog.RunID()
	}
	lastRunID := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if lineBelongsToTail(lines[i], currentRunID) {
			lastRunID = lines[i].RunID
			break
		}
	}
	var tail logTailEnvelope
	if lastRunID != "" {
		tail.LastRun = summarizeLogRun(lastRunID, lines)
	}
	var genericError *logErrorEnvelope
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !lineBelongsToTail(line, currentRunID) || line.Level != cklog.LevelError {
			continue
		}
		if genericError != nil && line.RunID != genericError.RunID {
			break
		}
		envelope := newLogErrorEnvelope(line)
		if line.Event == "run_failed" {
			genericError = envelope
			continue
		}
		tail.Error = envelope
		break
	}
	if tail.Error == nil {
		tail.Error = genericError
	}
	return tail
}

func lineBelongsToTail(line cklog.Line, currentRunID string) bool {
	return line.RunID != "" && line.RunID != "-" && line.RunID != currentRunID && line.Event != "grammar"
}

func summarizeLogRun(runID string, lines []cklog.Line) *logRunEnvelope {
	out := &logRunEnvelope{RunID: runID, Outcome: "running"}
	for _, line := range lines {
		if line.RunID != runID || line.Event == "grammar" {
			continue
		}
		if out.Command == "" {
			out.Command = line.Command
		}
		out.LastEvent = line.Event
		if out.StartedAt == "" || line.Event == "start" {
			out.StartedAt = formatTime(line.Timestamp)
		}
		fields := logMessageFields(line.Message)
		if line.Event == "start" {
			out.Version = fields["version"]
			out.Commit = fields["commit"]
			out.Platform = fields["platform"]
		}
		if line.Level == cklog.LevelError && out.Outcome == "running" {
			out.Outcome = "error"
		}
		if line.Event == "finish" {
			out.FinishedAt = formatTime(line.Timestamp)
			if fields["outcome"] != "" {
				out.Outcome = fields["outcome"]
			} else if line.Level == cklog.LevelError {
				out.Outcome = "error"
			} else {
				out.Outcome = "success"
			}
		}
	}
	return out
}

func newLogErrorEnvelope(line cklog.Line) *logErrorEnvelope {
	fields := logMessageFields(line.Message)
	message := line.Message
	if fields["error"] != "" {
		message = fields["error"]
	}
	return &logErrorEnvelope{
		RunID:   line.RunID,
		Command: line.Command,
		Event:   line.Event,
		Time:    formatTime(line.Timestamp),
		Message: message,
		Remedy:  fields["remedy"],
	}
}

func logMessageFields(message string) map[string]string {
	fields := map[string]string{}
	for i := 0; i < len(message); {
		for i < len(message) && unicode.IsSpace(rune(message[i])) {
			i++
		}
		keyStart := i
		for i < len(message) {
			r := rune(message[i])
			if r == '=' || unicode.IsSpace(r) {
				break
			}
			i++
		}
		if keyStart == i || i >= len(message) || message[i] != '=' {
			for i < len(message) && !unicode.IsSpace(rune(message[i])) {
				i++
			}
			continue
		}
		key := message[keyStart:i]
		i++
		value := ""
		if i < len(message) && message[i] == '"' {
			valueStart := i
			i++
			escaped := false
			closed := false
			for i < len(message) {
				switch {
				case escaped:
					escaped = false
				case message[i] == '\\':
					escaped = true
				case message[i] == '"':
					i++
					if unquoted, err := strconv.Unquote(message[valueStart:i]); err == nil {
						value = unquoted
					} else {
						value = message[valueStart:i]
					}
					closed = true
				}
				if closed {
					break
				}
				i++
			}
			if value == "" && valueStart < len(message) {
				value = strings.Trim(message[valueStart:i], `"`)
			}
		} else {
			valueStart := i
			for i < len(message) && !unicode.IsSpace(rune(message[i])) {
				i++
			}
			value = message[valueStart:i]
		}
		if key != "" {
			fields[key] = value
		}
	}
	return fields
}

func (a *app) withStore(ctx context.Context, fn func(*store.Store) error) error {
	st, err := store.Open(ctx, a.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

var errNoArchive = errors.New("no archive yet; run wacrawl sync to create it")

// withReadStore opens the archive read-only so read commands cannot
// change the archive file, per the reads-never-mutate contract rule.
func (a *app) withReadStore(ctx context.Context, fn func(*store.Store) error) error {
	st, err := store.OpenReadOnly(ctx, a.dbPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return worldMustChange(errNoArchive, "run wacrawl sync")
		}
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (a *app) withExistingStore(ctx context.Context, fn func(*store.Store) error) error {
	if _, err := os.Stat(a.dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return worldMustChange(errNoArchive, "run wacrawl sync")
		}
		return err
	}
	return a.withStore(ctx, fn)
}

func (a *app) runStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "status")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("status takes flags only"))
	}
	logTail := a.logTail()
	err := a.withReadStore(ctx, func(st *store.Store) error {
		status, err := st.Status(ctx)
		if err != nil {
			return err
		}
		if a.json {
			return a.print(newStatusEnvelope(status, logTail))
		}
		return a.printStatus(status, logTail)
	})
	if errors.Is(err, errNoArchive) {
		if a.json {
			return a.print(statusEnvelope{
				AppID:   "wacrawl",
				State:   "missing",
				Summary: errNoArchive.Error(),
				Counts:  []statusCount{},
				LastRun: logTail.LastRun,
				Error:   logTail.Error,
			})
		}
		return render.WriteStatus(a.stdout, render.Status{
			State:   render.StatusMissing,
			Summary: errNoArchive.Error(),
			Log:     renderLogTail(logTail),
		})
	}
	return err
}

func (a *app) runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source", a.source, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "doctor")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("doctor takes flags only"))
	}
	src, discoverErr := whatsappdb.Discover(ctx, *source)
	canaryRan := src.Available && strings.TrimSpace(src.ChatDB) != ""
	var canaryErr error
	if canaryRan {
		canaryErr = sourceCanary(ctx, src)
	}
	checks := []doctorCheck{
		sourceStoreCheck(src, discoverErr, canaryErr),
		a.archiveCheck(ctx),
	}
	if canaryRan {
		check, ok := fullDiskAccessCheck(canaryErr)
		if ok {
			checks = append(checks, check)
		}
	}
	logTail := a.logTail()
	return a.print(doctorEnvelope{Checks: checks, LastRun: logTail.LastRun, Error: logTail.Error})
}

func (a *app) runImport(ctx context.Context, command string, args []string) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source", a.source, "")
	copyMedia := fs.Bool("copy-media", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, command)
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(fmt.Errorf("%s takes flags only", command))
	}
	progress, stopProgress := a.importProgress(command)
	defer stopProgress()
	return a.withStore(ctx, func(st *store.Store) error {
		stats, err := whatsappdb.ImportWithOptions(ctx, st, whatsappdb.ImportOptions{SourcePath: *source, CopyMedia: *copyMedia, Progress: progress})
		if err != nil {
			return err
		}
		return a.print(stats)
	})
}

func (a *app) runContacts(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "export" {
		return usageErr(errors.New("contacts supports export only"))
	}
	fs := flag.NewFlagSet("contacts export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "contacts", "export")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("contacts export takes no arguments"))
	}
	return a.withReadStore(ctx, func(st *store.Store) error {
		contacts, err := st.Contacts(ctx)
		if err != nil {
			return err
		}
		export := control.ContactExport{Contacts: exportContacts(contacts)}
		if err := control.ValidateContactExport(export); err != nil {
			return err
		}
		return a.print(export)
	})
}

func exportContacts(contacts []store.Contact) []control.Contact {
	out := make([]control.Contact, 0, len(contacts))
	seen := map[string]struct{}{}
	for _, contact := range contacts {
		name := contactDisplayName(contact)
		phone := strings.TrimSpace(contact.Phone)
		if name == "" || phone == "" {
			continue
		}
		key := name + "\x00" + phone
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, control.Contact{DisplayName: name, PhoneNumbers: []string{phone}})
	}
	return out
}

type statusEnvelope struct {
	AppID     string             `json:"app_id"`
	State     string             `json:"state"`
	Summary   string             `json:"summary,omitempty"`
	Freshness *freshnessEnvelope `json:"freshness,omitempty"`
	Counts    []statusCount      `json:"counts"`
	LastRun   *logRunEnvelope    `json:"last_run,omitempty"`
	Error     *logErrorEnvelope  `json:"recent_error,omitempty"`
}

type doctorEnvelope struct {
	Checks  []doctorCheck     `json:"checks"`
	LastRun *logRunEnvelope   `json:"last_run,omitempty"`
	Error   *logErrorEnvelope `json:"recent_error,omitempty"`
}

type doctorCheck struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

func sourceStoreCheck(source whatsappdb.Source, discoverErr, canaryErr error) doctorCheck {
	check := doctorCheck{ID: "source_store"}
	chatDB := strings.TrimSpace(source.ChatDB)
	var chatDBStatErr error
	if chatDB != "" {
		_, chatDBStatErr = os.Stat(chatDB)
	}
	switch {
	case discoverErr != nil && isPermissionError(discoverErr):
		check.State = "ok"
		check.Message = "WhatsApp Desktop store path found"
	case discoverErr != nil:
		check.State = "fail"
		check.Message = discoverErr.Error()
		check.Remedy = "install WhatsApp Desktop, open it once, or pass --source PATH"
	case !source.Available:
		check.State = "missing"
		check.Message = "WhatsApp Desktop store was not found"
		check.Remedy = "install WhatsApp Desktop, open it once, or pass --source PATH"
	case chatDB == "":
		check.State = "missing"
		check.Message = "WhatsApp Desktop chat database was not found"
		check.Remedy = "open WhatsApp Desktop once, then run wacrawl sync"
	case errors.Is(chatDBStatErr, os.ErrNotExist):
		check.State = "missing"
		check.Message = "WhatsApp Desktop chat database was not found"
		check.Remedy = "open WhatsApp Desktop once, then run wacrawl sync"
	case chatDBStatErr != nil && !isPermissionError(chatDBStatErr):
		check.State = "fail"
		check.Message = chatDBStatErr.Error()
		check.Remedy = "check the WhatsApp Desktop store path, then run wacrawl doctor again"
	case canaryErr != nil && !isPermissionError(canaryErr):
		check.State = "fail"
		check.Message = "cannot read WhatsApp Desktop database: " + canaryErr.Error()
		check.Remedy = "close WhatsApp Desktop if it is busy, then run wacrawl doctor again"
	default:
		check.State = "ok"
		check.Message = "WhatsApp Desktop store found"
	}
	return check
}

func sourceCanary(ctx context.Context, source whatsappdb.Source) error {
	_, err := queryReadOnlySQL(ctx, source.ChatDB, "SELECT count(*) AS tables FROM sqlite_master")
	return err
}

func (a *app) archiveCheck(ctx context.Context) doctorCheck {
	check := doctorCheck{ID: "archive"}
	info, err := os.Stat(a.dbPath)
	switch {
	case strings.TrimSpace(a.dbPath) == "":
		check.State = "fail"
		check.Message = "archive database path is empty"
		check.Remedy = "pass --db PATH or run wacrawl sync with the default archive path"
	case err != nil && errors.Is(err, os.ErrNotExist):
		check.State = "missing"
		check.Message = "archive database does not exist"
		check.Remedy = "run wacrawl sync"
	case err != nil:
		check.State = "error"
		check.Message = err.Error()
		check.Remedy = "check the archive path, then run wacrawl sync"
	case info.IsDir():
		check.State = "fail"
		check.Message = "archive path is a directory"
		check.Remedy = "pass --db PATH pointing at a SQLite database, then run wacrawl sync"
	default:
		if _, err := queryReadOnlySQL(ctx, a.dbPath, "SELECT count(*) AS tables FROM sqlite_master"); err != nil {
			check.State = "error"
			check.Message = err.Error()
			check.Remedy = "move the corrupt archive aside, then run wacrawl sync"
			return check
		}
		check.State = "ok"
		check.Message = "archive database opened"
	}
	return check
}

func fullDiskAccessCheck(canaryErr error) (doctorCheck, bool) {
	check := doctorCheck{ID: "full_disk_access"}
	switch {
	case canaryErr == nil:
		check.State = "ok"
		check.Message = "source database canary read succeeded"
		return check, true
	case isPermissionError(canaryErr):
		check.State = "fail"
		check.Message = "cannot read the WhatsApp Desktop database"
		check.Remedy = "grant Full Disk Access to your terminal or Trawl in System Settings > Privacy & Security > Full Disk Access"
		return check, true
	default:
		return doctorCheck{}, false
	}
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "permission denied") ||
		strings.Contains(message, "operation not permitted") ||
		strings.Contains(message, "not authorized") ||
		strings.Contains(message, "authorization denied")
}

type freshnessEnvelope struct {
	LastSync string `json:"last_sync,omitempty"`
}

type statusCount struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value any    `json:"value"`
}

type logTailEnvelope struct {
	LastRun *logRunEnvelope
	Error   *logErrorEnvelope
}

type logRunEnvelope struct {
	RunID      string `json:"run_id"`
	Command    string `json:"command"`
	Outcome    string `json:"outcome"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	LastEvent  string `json:"last_event,omitempty"`
	Version    string `json:"version,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Platform   string `json:"platform,omitempty"`
}

type logErrorEnvelope struct {
	RunID   string `json:"run_id"`
	Command string `json:"command"`
	Event   string `json:"event"`
	Time    string `json:"time,omitempty"`
	Message string `json:"message"`
	Remedy  string `json:"remedy,omitempty"`
}

func newStatusEnvelope(status store.Status, logTail logTailEnvelope) statusEnvelope {
	state := "ok"
	summary := "archive ready"
	if status.Messages == 0 {
		state = "empty"
		if status.LastImportAt.IsZero() {
			summary = "archive is empty; run wacrawl sync to populate it"
		} else {
			summary = "archive contains no messages from the last sync"
		}
	}
	var freshness *freshnessEnvelope
	if !status.LastImportAt.IsZero() {
		freshness = &freshnessEnvelope{LastSync: formatTime(status.LastImportAt)}
	}
	var since any
	if !status.OldestMessage.IsZero() {
		since = status.OldestMessage.In(time.Local).Year()
	}
	return statusEnvelope{
		AppID:     "wacrawl",
		State:     state,
		Summary:   summary,
		Freshness: freshness,
		LastRun:   logTail.LastRun,
		Error:     logTail.Error,
		Counts: []statusCount{
			{ID: "messages", Label: "messages", Value: status.Messages},
			{ID: "chats", Label: "chats", Value: status.Chats},
			{ID: "since", Label: "since", Value: since},
		},
	}
}

type messageListOutput struct {
	Query     string          `json:"query,omitempty"`
	Returned  int             `json:"returned"`
	Limit     int             `json:"limit"`
	Truncated bool            `json:"truncated"`
	Messages  []store.Message `json:"results"`
}

func newMessageListOutput(query string, limit int, messages []store.Message) messageListOutput {
	if messages == nil {
		messages = []store.Message{}
	}
	return messageListOutput{
		Query:     query,
		Returned:  len(messages),
		Limit:     limit,
		Truncated: limit > 0 && len(messages) == limit,
		Messages:  messages,
	}
}

type searchEnvelope struct {
	Query        string         `json:"query"`
	WhoMatched   []string       `json:"who_matched,omitempty"`
	Results      []searchResult `json:"results"`
	TotalMatches int            `json:"total_matches"`
	Truncated    bool           `json:"truncated"`
}

type searchResult struct {
	Ref     string `json:"ref"`
	Alias   string `json:"-"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}

type openEnvelope struct {
	Ref     string            `json:"ref"`
	Chat    string            `json:"chat"`
	Message openMessage       `json:"message"`
	Context []openMessage     `json:"context"`
	Window  openWindowSummary `json:"window"`
}

type openWindowSummary struct {
	Before int `json:"before"`
	After  int `json:"after"`
}

type openMessage struct {
	Ref     string     `json:"ref"`
	Time    string     `json:"time"`
	Who     string     `json:"who"`
	Where   string     `json:"where"`
	Text    string     `json:"text"`
	Type    string     `json:"type,omitempty"`
	Media   *openMedia `json:"media,omitempty"`
	Starred bool       `json:"starred,omitempty"`
	Current bool       `json:"current,omitempty"`
}

type openMedia struct {
	Type      string `json:"type,omitempty"`
	Title     string `json:"title,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

type errorEnvelope struct {
	Error contractError `json:"error"`
}

type contractError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Remedy  string `json:"remedy"`
}

type contractFailure struct {
	contractError
}

func (e *contractFailure) Error() string {
	return e.Message
}

func contactDisplayName(contact store.Contact) string {
	for _, name := range []string{
		contact.FullName,
		contact.BusinessName,
		strings.TrimSpace(contact.FirstName + " " + contact.LastName),
	} {
		if cleaned := cleanContactName(name, contact); cleaned != "" {
			return cleaned
		}
	}
	return ""
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

func (a *app) runChats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	unread := fs.Bool("unread", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "chats")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("chats takes flags only"))
	}
	return a.withReadStore(ctx, func(st *store.Store) error {
		var (
			chats []store.Chat
			err   error
		)
		if *unread {
			chats, err = st.ListUnreadChats(ctx, *limit)
		} else {
			chats, err = st.ListChats(ctx, *limit)
		}
		if err != nil {
			return err
		}
		return a.print(chats)
	})
}

func (a *app) runUnread(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unread", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "unread")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("unread takes flags only"))
	}
	return a.withReadStore(ctx, func(st *store.Store) error {
		chats, err := st.ListUnreadChats(ctx, *limit)
		if err != nil {
			return err
		}
		return a.print(chats)
	})
}

func (a *app) runMessages(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	filter := bindMessageFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "messages")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("messages takes flags only"))
	}
	resolved, err := filter.resolve()
	if err != nil {
		return usageErr(err)
	}
	return a.withReadStore(ctx, func(st *store.Store) error {
		msgs, err := st.Messages(ctx, resolved)
		if err != nil {
			return err
		}
		return a.print(newMessageListOutput("", resolved.Limit, msgs))
	})
}

func (a *app) runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	filter := bindSearchFlags(fs)
	if commandWantsHelp(args) {
		printCommandUsage(a.stdout, "search")
		return nil
	}
	flagArgs, query, err := splitSearchArgs(args)
	if err != nil {
		return usageErr(err)
	}
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "search")
			return nil
		}
		return usageErr(err)
	}
	resolved, err := filter.resolve(flagWasProvided(fs, "who"))
	if err != nil {
		return usageErr(err)
	}
	resolved.Query = query
	withStore := a.withReadStore
	if !a.json {
		withStore = a.withExistingStore
	}
	return withStore(ctx, func(st *store.Store) error {
		var whoMatched []string
		if resolved.Who != "" {
			resolution, err := st.ResolveWho(ctx, resolved.Who)
			if err != nil {
				return err
			}
			resolved.WhoKeys = resolution.ParticipantKeys
			whoMatched = resolution.DisplayNames
		}
		total, err := st.SearchCount(ctx, resolved)
		if err != nil {
			return err
		}
		msgs, err := st.Search(ctx, resolved)
		if err != nil {
			return err
		}
		aliases := map[string]string{}
		if !a.json {
			if err := st.EnsureShortRefs(ctx); err != nil {
				return err
			}
			aliases, err = st.ShortRefAliases(ctx, messageRefs(msgs))
			if err != nil {
				return err
			}
		}
		return a.print(newSearchEnvelope(query, total, msgs, whoMatched, aliases))
	})
}

func (a *app) runOpen(ctx context.Context, args []string) error {
	if commandWantsHelp(args) {
		printCommandUsage(a.stdout, "open")
		return nil
	}
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "open")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 1 {
		return usageErr(errors.New("open requires exactly one ref"))
	}
	ref := strings.TrimSpace(fs.Arg(0))
	if strings.Contains(ref, ":") {
		messageID, contractErr := parseMessageRef(ref)
		if contractErr != nil {
			return a.failContract(*contractErr)
		}
		return a.withReadStore(ctx, func(st *store.Store) error {
			return a.openMessageID(ctx, st, messageID)
		})
	}
	if !shortref.ValidAlias(ref) {
		return a.failContract(unknownShortRefError())
	}
	return a.withExistingStore(ctx, func(st *store.Store) error {
		if err := st.EnsureShortRefs(ctx); err != nil {
			return err
		}
		fullRefs, err := st.ResolveShortRef(ctx, ref)
		if err != nil {
			return err
		}
		switch len(fullRefs) {
		case 0:
			return a.failContract(unknownShortRefError())
		case 1:
			messageID, contractErr := parseMessageRef(fullRefs[0])
			if contractErr != nil {
				return a.failContract(*contractErr)
			}
			return a.openMessageID(ctx, st, messageID)
		default:
			return a.failContract(contractError{
				Code:    "ambiguous_short_ref",
				Message: "short ref matches more than one message",
				Remedy:  "rerun wacrawl search or use the full ref",
			})
		}
	})
}

func (a *app) openMessageID(ctx context.Context, st *store.Store, messageID string) error {
	target, err := st.MessageByID(ctx, messageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return a.failContract(contractError{
				Code:    "not_found",
				Message: "message was not found",
				Remedy:  "run wacrawl search again and pass one of its refs",
			})
		}
		return err
	}
	window, err := st.MessageWindow(ctx, target, openWindowEachSide)
	if err != nil {
		return err
	}
	return a.print(newOpenEnvelope(target, window))
}

func unknownShortRefError() contractError {
	return contractError{
		Code:    "unknown_short_ref",
		Message: "short ref was not found",
		Remedy:  "use a full ref from wacrawl search",
	}
}

func parseMessageRef(ref string) (string, *contractError) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, messageRefPrefix) {
		return "", &contractError{
			Code:    "foreign_ref",
			Message: "ref does not belong to wacrawl",
			Remedy:  "pass a ref returned by wacrawl search",
		}
	}
	messageID := strings.TrimSpace(strings.TrimPrefix(ref, messageRefPrefix))
	if messageID == "" {
		return "", &contractError{
			Code:    "invalid_ref",
			Message: "wacrawl message ref is missing its message id",
			Remedy:  "pass a complete ref returned by wacrawl search",
		}
	}
	return messageID, nil
}

func splitSearchArgs(args []string) ([]string, string, error) {
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if searchFlagNeedsValue(arg) && !strings.Contains(arg, "=") {
				next := i + 1
				if next >= len(args) {
					return nil, "", fmt.Errorf("flag needs an argument: %s", arg)
				}
				flags = append(flags, args[next]) // #nosec G602 -- next is checked against len(args) above.
				i = next
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) != 1 {
		return nil, "", errors.New("search requires exactly one query")
	}
	return flags, positionals[0], nil
}

func searchFlagNeedsValue(arg string) bool {
	name := strings.TrimPrefix(arg, "-")
	name = strings.TrimPrefix(name, "-")
	if before, _, ok := strings.Cut(name, "="); ok {
		name = before
	}
	switch name {
	case "chat", "sender", "who", "limit", "after", "before":
		return true
	default:
		return false
	}
}

type messageFlags struct {
	chat     *string
	sender   *string
	limit    *int
	after    *string
	before   *string
	fromMe   *bool
	fromThem *bool
	hasMedia *bool
	asc      *bool
}

type searchFlags struct {
	messageFlags
	who *string
}

func bindMessageFlags(fs *flag.FlagSet) messageFlags {
	return messageFlags{
		chat:     fs.String("chat", "", ""),
		sender:   fs.String("sender", "", ""),
		limit:    fs.Int("limit", defaultMessageLimit, ""),
		after:    fs.String("after", "", ""),
		before:   fs.String("before", "", ""),
		fromMe:   fs.Bool("from-me", false, ""),
		fromThem: fs.Bool("from-them", false, ""),
		hasMedia: fs.Bool("has-media", false, ""),
		asc:      fs.Bool("asc", false, ""),
	}
}

func bindSearchFlags(fs *flag.FlagSet) searchFlags {
	return searchFlags{
		messageFlags: bindMessageFlags(fs),
		who:          fs.String("who", "", ""),
	}
}

func (f messageFlags) resolve() (store.MessageFilter, error) {
	if *f.fromMe && *f.fromThem {
		return store.MessageFilter{}, errors.New("--from-me and --from-them are mutually exclusive")
	}
	if *f.limit < 1 || *f.limit > maxMessageLimit {
		return store.MessageFilter{}, fmt.Errorf("--limit must be between 1 and %d", maxMessageLimit)
	}
	out := store.MessageFilter{
		ChatJID:  *f.chat,
		Sender:   *f.sender,
		Limit:    *f.limit,
		HasMedia: *f.hasMedia,
		Asc:      *f.asc,
	}
	if *f.fromMe {
		v := true
		out.FromMe = &v
	}
	if *f.fromThem {
		v := false
		out.FromMe = &v
	}
	if strings.TrimSpace(*f.after) != "" {
		t, err := parseTime(*f.after)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.After = &t
	}
	if strings.TrimSpace(*f.before) != "" {
		t, err := parseTime(*f.before)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.Before = &t
	}
	return out, nil
}

func (f searchFlags) resolve(whoProvided bool) (store.MessageFilter, error) {
	out, err := f.messageFlags.resolve()
	if err != nil {
		return store.MessageFilter{}, err
	}
	if !whoProvided {
		return out, nil
	}
	out.Who = normalizeWhoValue(*f.who)
	if out.Who == "" {
		return store.MessageFilter{}, errors.New("--who requires an identity")
	}
	return out, nil
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			provided = true
		}
	})
	return provided
}

func normalizeWhoValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (a *app) print(value any) error {
	if a.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	switch v := value.(type) {
	case store.ImportStats:
		_, err := fmt.Fprintf(a.stdout, "source=%s\ndb=%s\nchats=%d\ncontacts=%d\ngroups=%d\nparticipants=%d\nmessages=%d\nmedia_messages=%d\nmedia_copied=%d\nmedia_missing=%d\n",
			v.SourcePath, v.DBPath, v.Chats, v.Contacts, v.Groups, v.Participants, v.Messages, v.MediaMessages, v.MediaCopied, v.MediaMissing)
		return err
	case store.Status:
		return a.printStatus(v, logTailEnvelope{})
	case []store.Chat:
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "LAST\tKIND\tUNREAD\tMESSAGES\tJID\tNAME")
		for _, c := range v {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n", formatTime(c.LastMessageAt), c.Kind, c.UnreadCount, c.MessageCount, c.JID, c.Name)
		}
		return tw.Flush()
	case []store.Message:
		return a.printMessages(v, false, 0)
	case messageListOutput:
		return a.printMessages(v.Messages, v.Truncated, v.Limit)
	case searchEnvelope:
		return a.printSearch(v)
	case openEnvelope:
		return a.printOpen(v)
	case doctorEnvelope:
		return a.printDoctor(v)
	case sqlQueryResult:
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, strings.Join(v.columns, "\t"))
		for _, row := range v.rows {
			values := make([]string, 0, len(v.columns))
			for _, col := range v.columns {
				values = append(values, formatSQLValue(row[col]))
			}
			_, _ = fmt.Fprintln(tw, strings.Join(values, "\t"))
		}
		return tw.Flush()
	case backup.Result:
		_, err := fmt.Fprintf(a.stdout, "repo=%s\nchanged=%t\nencrypted=%t\nshards=%d\nmessages=%d\nmedia_files=%d\n", v.Repo, v.Changed, v.Encrypted, v.Shards, v.Messages, v.MediaFiles)
		if err == nil && v.Ref != "" {
			_, err = fmt.Fprintf(a.stdout, "ref=%s\n", v.Ref)
		}
		if err == nil && v.Tag != "" {
			_, err = fmt.Fprintf(a.stdout, "tag=%s\n", v.Tag)
		}
		return err
	case []backup.Snapshot:
		if len(v) == 0 {
			_, err := fmt.Fprintln(a.stdout, "No backup snapshots found.")
			return err
		}
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "REF\tEXPORTED\tMESSAGES\tMEDIA\tSHARDS\tTAGS")
		for _, snapshot := range v {
			ref := snapshot.Ref
			if len(ref) > 12 {
				ref = ref[:12]
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\n", ref, formatTime(snapshot.Exported), snapshot.Counts.Messages, snapshot.Counts.MediaFiles, snapshot.Shards, strings.Join(snapshot.Tags, ","))
		}
		return tw.Flush()
	case backup.Manifest:
		_, err := fmt.Fprintf(a.stdout, "encrypted=%t\nshards=%d\nmessages=%d\nmedia_files=%d\nexported=%s\n", v.Encrypted, len(v.Shards), v.Counts.Messages, len(v.Files), formatTime(v.Exported))
		return err
	default:
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
}

func (a *app) printStatus(status store.Status, logTail logTailEnvelope) error {
	return render.WriteStatus(a.stdout, renderStatus(status, logTail))
}

func renderStatus(status store.Status, logTail logTailEnvelope) render.Status {
	envelope := newStatusEnvelope(status, logTail)
	sections := []render.Section{
		{Title: "Archive", Fields: []render.Field{
			{Label: "Database", Value: status.DBPath},
			{Label: "Source", Value: status.LastSource},
		}},
		{Title: "Messages", Fields: []render.Field{
			{Label: "Messages", Value: strconv.Itoa(status.Messages)},
			{Label: "Media messages", Value: strconv.Itoa(status.MediaMessages)},
			{Label: "Chats", Value: strconv.Itoa(status.Chats)},
			{Label: "Unread chats", Value: strconv.Itoa(status.UnreadChats)},
			{Label: "Unread messages", Value: strconv.Itoa(status.UnreadMessages)},
			{Label: "Contacts", Value: strconv.Itoa(status.Contacts)},
			{Label: "Groups", Value: strconv.Itoa(status.Groups)},
			{Label: "Participants", Value: strconv.Itoa(status.Participants)},
		}},
	}
	if !status.OldestMessage.IsZero() || !status.NewestMessage.IsZero() {
		sections = append(sections, render.Section{Title: "Range", Fields: []render.Field{
			{Label: "Oldest message", Value: formatTime(status.OldestMessage)},
			{Label: "Newest message", Value: formatTime(status.NewestMessage)},
		}})
	}
	var freshness *render.Freshness
	if envelope.Freshness != nil {
		freshness = &render.Freshness{LastSync: envelope.Freshness.LastSync}
	}
	return render.Status{
		State:     renderStatusState(envelope.State),
		Summary:   envelope.Summary,
		Sections:  sections,
		Freshness: freshness,
		Log:       renderLogTail(logTail),
	}
}

func renderStatusState(state string) render.StatusState {
	switch state {
	case "ok":
		return render.StatusOK
	case "empty":
		return render.StatusEmpty
	case "missing":
		return render.StatusMissing
	case "error":
		return render.StatusError
	default:
		return render.StatusUnknown
	}
}

func (a *app) printDoctor(envelope doctorEnvelope) error {
	checks := make([]render.Check, 0, len(envelope.Checks))
	for _, check := range envelope.Checks {
		checks = append(checks, render.Check{
			Name:    doctorCheckLabel(check.ID),
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return render.WriteDoctor(a.stdout, checks, renderLogTail(logTailEnvelope{LastRun: envelope.LastRun, Error: envelope.Error}))
}

func doctorCheckLabel(id string) string {
	switch id {
	case "source_store":
		return "source store"
	case "full_disk_access":
		return "full disk access"
	default:
		return strings.ReplaceAll(id, "_", " ")
	}
}

func renderLogTail(tail logTailEnvelope) render.LogTail {
	return render.LogTail{
		LastRun:         renderLogRun(tail.LastRun),
		MostRecentError: renderLogError(tail.Error),
	}
}

func renderLogRun(run *logRunEnvelope) *cklog.RunSummary {
	if run == nil {
		return nil
	}
	return &cklog.RunSummary{
		Command:    run.Command,
		StartedAt:  parseFormattedTime(run.StartedAt),
		FinishedAt: parseFormattedTime(run.FinishedAt),
		Outcome:    humanLogOutcome(run.Outcome),
		LastEvent:  run.LastEvent,
		Version:    run.Version,
		Commit:     run.Commit,
		Platform:   run.Platform,
	}
}

func renderLogError(logError *logErrorEnvelope) *cklog.Line {
	if logError == nil {
		return nil
	}
	message := ""
	if strings.TrimSpace(logError.Message) != "" {
		message = "error=" + strconv.Quote(logError.Message)
	}
	if strings.TrimSpace(logError.Remedy) != "" {
		if message != "" {
			message += " "
		}
		message += "remedy=" + strconv.Quote(logError.Remedy)
	}
	return &cklog.Line{
		Timestamp: parseFormattedTime(logError.Time),
		Command:   logError.Command,
		Event:     logError.Event,
		Message:   message,
	}
}

func parseFormattedTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func humanLogOutcome(outcome string) string {
	switch outcome {
	case "success":
		return "succeeded"
	case "error":
		return "failed"
	default:
		return outcome
	}
}

func (a *app) printSearch(result searchEnvelope) error {
	if len(result.WhoMatched) > 1 {
		if _, err := fmt.Fprintf(a.stdout, "who matched: %s\n\n", strings.Join(result.WhoMatched, ", ")); err != nil {
			return err
		}
	}
	for _, item := range result.Results {
		if item.Alias != "" {
			if _, err := fmt.Fprintf(a.stdout, "%s  %s in %s\n%s\nref: %s\nfull ref: %s\n\n", item.Time, item.Who, item.Where, item.Snippet, item.Alias, item.Ref); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(a.stdout, "%s  %s in %s\n%s\nref: %s\n\n", item.Time, item.Who, item.Where, item.Snippet, item.Ref); err != nil {
				return err
			}
		}
	}
	if result.Truncated {
		_, err := fmt.Fprintf(a.stdout, "showing %d of %d matches; narrow with --limit, --after, --before, or --chat\n", len(result.Results), result.TotalMatches)
		return err
	}
	_, err := fmt.Fprintf(a.stdout, "showing %d of %d matches\n", len(result.Results), result.TotalMatches)
	return err
}

func (a *app) printOpen(result openEnvelope) error {
	if _, err := fmt.Fprintf(a.stdout, "chat: %s\nref: %s\n\n", result.Chat, result.Ref); err != nil {
		return err
	}
	for _, item := range result.Context {
		marker := " "
		if item.Current {
			marker = ">"
		}
		if _, err := fmt.Fprintf(a.stdout, "%s [%s] %s: %s\n", marker, item.Time, item.Who, item.Text); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) printMessages(messages []store.Message, truncated bool, limit int) error {
	for _, m := range messages {
		body := firstNonEmpty(messageSnippet(m), messageText(m))
		if _, err := fmt.Fprintf(a.stdout, "[%s] %s / %s / %s\n%s\n\n", formatTime(m.Timestamp), m.ChatName, firstNonEmpty(m.SenderName, m.SenderJID), m.MessageID, body); err != nil {
			return err
		}
	}
	if truncated {
		_, err := fmt.Fprintf(a.stdout, "showing %d of possibly more; narrow with --limit, --after, --before, or --chat\n", limit)
		return err
	}
	return nil
}

func newSearchEnvelope(query string, total int, messages []store.Message, whoMatched []string, aliases map[string]string) searchEnvelope {
	if messages == nil {
		messages = []store.Message{}
	}
	results := make([]searchResult, 0, len(messages))
	for _, message := range messages {
		fullRef := messageRef(message)
		results = append(results, newSearchResult(message, aliases[fullRef]))
	}
	return searchEnvelope{
		Query:        query,
		WhoMatched:   ambiguousWhoMatches(whoMatched),
		Results:      results,
		TotalMatches: total,
		Truncated:    total > len(results),
	}
}

func ambiguousWhoMatches(names []string) []string {
	if len(names) <= 1 {
		return nil
	}
	return names
}

func newSearchResult(message store.Message, alias string) searchResult {
	return searchResult{
		Ref:     messageRef(message),
		Alias:   alias,
		Time:    formatTime(message.Timestamp),
		Who:     outputField(messageWho(message)),
		Where:   outputField(messageWhere(message)),
		Snippet: outputField(messageSnippet(message)),
	}
}

func newOpenEnvelope(target store.Message, context []store.Message) openEnvelope {
	openContext := make([]openMessage, 0, len(context))
	before := 0
	after := 0
	for _, message := range context {
		current := message.SourcePK == target.SourcePK
		if current {
			openContext = append(openContext, newOpenMessage(message, true))
			continue
		}
		if message.Timestamp.Before(target.Timestamp) || (message.Timestamp.Equal(target.Timestamp) && message.SourcePK < target.SourcePK) {
			before++
		} else {
			after++
		}
		openContext = append(openContext, newOpenMessage(message, false))
	}
	return openEnvelope{
		Ref:     messageRef(target),
		Chat:    messageWhere(target),
		Message: newOpenMessage(target, true),
		Context: openContext,
		Window:  openWindowSummary{Before: before, After: after},
	}
}

func newOpenMessage(message store.Message, current bool) openMessage {
	media := messageMedia(message)
	return openMessage{
		Ref:     messageRef(message),
		Time:    formatTime(message.Timestamp),
		Who:     outputField(messageWho(message)),
		Where:   outputField(messageWhere(message)),
		Text:    messageText(message),
		Type:    messageKind(message),
		Media:   media,
		Starred: message.Starred,
		Current: current,
	}
}

func messageMedia(message store.Message) *openMedia {
	kind := ""
	if messageCarriesMedia(message) {
		kind = messageKind(message)
	} else {
		kind = normalizeMessageKind(message.MediaType)
	}
	title := safeMediaTitle(message)
	if kind == "" && title == "" && message.MediaSize == 0 {
		return nil
	}
	return &openMedia{Type: kind, Title: title, SizeBytes: message.MediaSize}
}

func messageRef(message store.Message) string {
	return messageRefPrefix + message.MessageID
}

func messageRefs(messages []store.Message) []string {
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		refs = append(refs, messageRef(message))
	}
	return refs
}

func messageWho(message store.Message) string {
	if message.FromMe {
		return "me"
	}
	if name := humanDisplayName(message.SenderName); name != "" {
		return name
	}
	return "Unknown sender"
}

func messageWhere(message store.Message) string {
	if name := humanDisplayName(message.ChatName); name != "" {
		return name
	}
	return "Unknown chat"
}

func messageSnippet(message store.Message) string {
	if snippet := outputField(message.Snippet); snippet != "" && !containsOpaqueMediaReference(message, snippet) {
		return snippet
	}
	return outputField(messageText(message))
}

func messageText(message store.Message) string {
	if text := outputField(message.Text); text != "" && !containsOpaqueMediaReference(message, text) {
		return text
	}
	if !messageCarriesMedia(message) {
		if title := safeMediaTitle(message); title != "" {
			return title
		}
	}
	return readableMessageType(message)
}

func safeMediaTitle(message store.Message) string {
	if title := safeMediaLabel(message.MediaTitle); title != "" {
		return title
	}
	return safeMediaFilename(message.MediaPath)
}

func safeMediaFilename(mediaPath string) string {
	mediaPath = strings.TrimSpace(mediaPath)
	if mediaPath == "" {
		return ""
	}
	return safeMediaLabel(filepath.Base(mediaPath))
}

func safeMediaLabel(value string) string {
	value = outputField(value)
	if value == "" || value == "." || value == "/" || value == `\` {
		return ""
	}
	for _, field := range strings.Fields(value) {
		if opaqueMediaToken(field) {
			return ""
		}
		stem := strings.TrimSuffix(field, filepath.Ext(field))
		if opaqueMediaToken(stem) {
			return ""
		}
	}
	return value
}

func readableMessageType(message store.Message) string {
	kind := messageKind(message)
	if kind == "" && (message.RawType != 0 || message.MessageType != "" || message.MediaType != "") {
		return "[unsupported message]"
	}
	if kind == "" {
		return ""
	}
	return "[" + strings.ReplaceAll(kind, "_", " ") + "]"
}

func messageKind(message store.Message) string {
	for _, kind := range []string{message.MediaType, message.MessageType} {
		kind = normalizeMessageKind(kind)
		if kind != "" {
			return kind
		}
	}
	return knownMessageType(message.RawType)
}

func normalizeMessageKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || numericInternalKind(kind) {
		return ""
	}
	return kind
}

func knownMessageType(raw int) string {
	switch raw {
	case 0:
		return "text"
	case 1:
		return "image"
	case 2:
		return "video"
	case 3:
		return "audio"
	case 4:
		return "location"
	case 5:
		return "contact"
	case 6:
		return "system"
	case 7:
		return "link"
	case 8:
		return "document"
	case 10:
		return "group_event"
	case 11:
		return "gif"
	case 14:
		return "reaction"
	case 15:
		return "sticker"
	case 59:
		return "status_update"
	default:
		return ""
	}
}

func numericInternalKind(kind string) bool {
	for _, prefix := range []string{"type_", "status_"} {
		if suffix, ok := strings.CutPrefix(kind, prefix); ok {
			return allDigits(suffix)
		}
	}
	return false
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func containsOpaqueMediaReference(message store.Message, value string) bool {
	if !messageCarriesMedia(message) {
		return false
	}
	for _, field := range strings.Fields(value) {
		if opaqueMediaToken(field) {
			return true
		}
	}
	return false
}

func messageCarriesMedia(message store.Message) bool {
	switch messageKind(message) {
	case "image", "video", "audio", "document", "gif", "sticker":
		return true
	}
	return message.MediaPath != "" || message.MediaURL != "" || message.MediaSize > 0
}

func opaqueMediaToken(value string) bool {
	value = strings.Trim(value, `"'.,;:()[]{}<>`)
	if len(value) < 40 {
		return false
	}
	allHex := true
	allBase64 := true
	hasBase64Mark := false
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			allHex = false
		}
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '+', r == '/', r == '_', r == '-', r == '=':
			hasBase64Mark = true
		default:
			allBase64 = false
		}
	}
	return allHex || (allBase64 && (hasBase64Mark || len(value)%4 == 0))
}

func humanDisplayName(name string) string {
	name = outputField(name)
	if strings.EqualFold(name, "me") {
		return "me"
	}
	if name == "" || strings.Contains(name, "@") || looksLikePhone(name) {
		return ""
	}
	return name
}

func outputField(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (a *app) failContract(contractErr contractError) error {
	if a.json {
		if err := a.print(errorEnvelope{Error: contractErr}); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintf(a.stderr, "%s. %s.\n", contractErr.Message, contractErr.Remedy)
	}
	failure := &contractFailure{contractError: contractErr}
	return &cliError{code: 1, err: cklog.WorldMustChange{Err: failure, Message: contractErr.Message, Remedy: contractErr.Remedy}}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `wacrawl reads local WhatsApp Desktop data into a readonly archive.

Usage:
  wacrawl [--db PATH] [--source PATH] [--json] <command> [args]
  wacrawl --version

Commands:
  metadata    Print crawlkit control metadata.
  doctor      Inspect WhatsApp Desktop source and archive paths.
  import      Snapshot WhatsApp Desktop SQLite data into the archive.
  sync        Alias for import.
  status      Show archive status.
  chats       List chats.
  contacts    Export archived contacts.
  unread      List chats with unread messages.
  messages    List archived messages.
  search      Search archived messages.
  open        Open a message ref from search.
  sql         Run a read-only SQL query.
  web         Browse the local archive in a private web viewer.
  backup      Init, push, pull, or inspect encrypted Git backups.

Options:
  --db PATH                 Archive database path.
  --source PATH             WhatsApp Desktop source path.
  --json                    Emit JSON output.
  --version                 Print the CLI version.

Import flags:
  --copy-media              Copy referenced media files into the archive media directory.

Examples:
  wacrawl doctor
  wacrawl sync
  wacrawl unread --limit 20
  wacrawl --json contacts export
  wacrawl --json search "invoice" --from-them --after 2026-01-01
  wacrawl open wacrawl:msg/MESSAGE_ID
  wacrawl sql "SELECT count(*) FROM messages"
  wacrawl web
  wacrawl help messages
`)
}

func commandWantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func printCommandUsage(w io.Writer, topic ...string) bool {
	name := strings.Join(topic, " ")
	switch name {
	case "doctor":
		_, _ = fmt.Fprint(w, `Inspect the WhatsApp Desktop source and archive paths.

Usage:
  wacrawl doctor [--source PATH]

Flags:
  --source PATH   WhatsApp Desktop source path.

Examples:
  wacrawl doctor
  wacrawl --json doctor
`)
	case "import", "sync":
		_, _ = fmt.Fprintf(w, `Snapshot WhatsApp Desktop SQLite data into the archive.

Usage:
  wacrawl %s [--source PATH] [--copy-media]

Flags:
  --source PATH   WhatsApp Desktop source path.
  --copy-media    Copy referenced media files into media/ next to the archive DB.

Examples:
  wacrawl %s
  wacrawl %s --copy-media
  wacrawl --db /tmp/wacrawl.db %s
`, name, name, name, name)
	case "status":
		_, _ = fmt.Fprint(w, `Show archive status, counts, date span, unread counts, and last import metadata.

Usage:
  wacrawl status

Examples:
  wacrawl status
  wacrawl --json status
`)
	case "chats":
		_, _ = fmt.Fprint(w, `List archived chats.

Usage:
  wacrawl chats [--limit N] [--unread]

Flags:
  --limit N   Maximum chats to print. Default: 50.
  --unread    Show only chats with unread messages.

Examples:
  wacrawl chats --limit 20
  wacrawl chats --unread
  wacrawl --json chats --limit 100
`)
	case "contacts", "contacts export":
		_, _ = fmt.Fprint(w, `Export archived contacts.

Usage:
  wacrawl [--json] contacts export

Examples:
  wacrawl --json contacts export
`)
	case "unread":
		_, _ = fmt.Fprint(w, `List chats with unread messages.

Usage:
  wacrawl unread [--limit N]

Flags:
  --limit N   Maximum chats to print. Default: 50.

Examples:
  wacrawl unread
  wacrawl unread --limit 20
`)
	case "messages":
		_, _ = fmt.Fprint(w, `List archived messages.

Usage:
  wacrawl messages [flags]

Flags:
  --chat JID       Filter by chat JID.
  --sender JID     Filter by sender JID.
  --limit N        Maximum messages to print. Default: 20, maximum: 200.
  --after TIME     Only messages after RFC3339 or YYYY-MM-DD.
  --before TIME    Only messages before RFC3339 or YYYY-MM-DD.
  --from-me        Only messages sent by me.
  --from-them      Only messages received from others.
  --has-media      Only messages with media metadata.
  --asc            Show oldest messages first.

Examples:
  wacrawl messages --limit 20
  wacrawl messages --chat 1234567890@s.whatsapp.net --asc
  wacrawl messages --after 2026-01-01 --from-them
  wacrawl --json messages --has-media --limit 100
`)
	case "search":
		_, _ = fmt.Fprint(w, `Search archived messages.

Usage:
  wacrawl search [flags] <query>
  wacrawl search <query> [flags]

Flags:
  --chat JID       Filter by chat JID.
  --sender JID     Filter by sender JID.
  --who NAME       Filter to messages where NAME is a sender, recipient, or chat member.
  --limit N        Maximum messages to print. Default: 20, maximum: 200.
  --after TIME     Only messages after RFC3339 or YYYY-MM-DD.
  --before TIME    Only messages before RFC3339 or YYYY-MM-DD.
  --from-me        Only messages sent by me.
  --from-them      Only messages received from others.
  --has-media      Only messages with media metadata.
  --asc            Show oldest messages first.

Examples:
  wacrawl search "invoice"
  wacrawl search "invoice" --who "Alice Example"
  wacrawl search "flight" --after 2026-01-01 --from-them
  wacrawl --json search --chat 1234567890@s.whatsapp.net "release notes"
`)
	case "open":
		_, _ = fmt.Fprint(w, `Open an archived message by ref.

Usage:
  wacrawl open <ref>

The ref must come from wacrawl search. Use the short ref from text output or
the full ref that looks like wacrawl:msg/MESSAGE_ID.

Examples:
  wacrawl open abc23
  wacrawl open wacrawl:msg/MESSAGE_ID
  wacrawl --json open wacrawl:msg/MESSAGE_ID
`)
	case "sql":
		_, _ = fmt.Fprint(w, `Run a read-only SQL query against the archive database.

Usage:
  wacrawl sql <select query>

Examples:
  wacrawl sql "SELECT count(*) FROM messages"
  wacrawl --json sql "SELECT chat_jid, count(*) FROM messages GROUP BY chat_jid"
`)
	case "web":
		_, _ = fmt.Fprint(w, `Browse the local archive in a private web viewer.

The viewer binds only to 127.0.0.1 and requires a random key printed in its URL.
It reads archive status, chats, messages, and search results without serving media
files or exposing configuration and write controls.

Usage:
  wacrawl web [--port N]

Flags:
  --port N   Loopback port. Default: choose a free random port.

Examples:
  wacrawl web
  wacrawl web --port 8787
`)
	case "backup":
		_, _ = fmt.Fprint(w, `Manage encrypted Git backups of the wacrawl archive.

Usage:
  wacrawl backup <init|push|pull|status|snapshots> [flags]

Commands:
  init      Create backup config, age identity, and first encrypted backup.
  push      Export the archive and push encrypted shards.
  pull      Restore encrypted shards into the configured archive DB.
  status    Inspect backup config and manifest.
  snapshots List restorable Git backup snapshots and tags.

Common flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.

Examples:
  wacrawl backup status
  wacrawl backup snapshots
  wacrawl backup push
  wacrawl backup init --repo ~/Projects/backup-wacrawl --remote https://github.com/steipete/backup-wacrawl.git
`)
	case "backup init":
		_, _ = fmt.Fprint(w, `Initialize encrypted Git backup configuration.

Usage:
  wacrawl backup init [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.
`)
	case "backup push":
		_, _ = fmt.Fprint(w, `Export and push encrypted archive shards and copied media.

Usage:
  wacrawl backup push [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.
  --no-media         Omit copied media files from this backup.
  --tag NAME         Tag the resulting backup commit.
`)
	case "backup pull":
		_, _ = fmt.Fprint(w, `Restore encrypted archive shards and copied media.

Usage:
  wacrawl backup pull [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --no-media         Restore archive rows without copied media files.
  --ref REF          Restore a tag, commit, or branch without changing checkout.
`)
	case "backup status":
		_, _ = fmt.Fprint(w, `Show encrypted backup status and manifest metadata.

Usage:
  wacrawl backup status [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
`)
	case "backup snapshots":
		_, _ = fmt.Fprint(w, `List restorable encrypted backup snapshots from Git history.

Usage:
  wacrawl backup snapshots [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --limit N          Maximum snapshots to list. Default: 20.
`)
	default:
		return false
	}
	return true
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "wacrawl.db"
	}
	return filepath.Join(home, ".wacrawl", "wacrawl.db")
}

func usageErr(err error) error {
	return &cliError{code: 2, err: err}
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty time")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q", value)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(time.Local).Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
