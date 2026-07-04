// Package log writes crawl run logs in the shared OpenTrawl grammar.
package log

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	Grammar = "<timestamp> <level> <run-id> <command> <event>: <message>"

	currentLogName           = "current.log"
	defaultRotationLimitByte = 4 * 1024 * 1024
	defaultProgressLogEvery  = 30 * time.Second
	logTimeLayout            = "2006-01-02 15:04:05"
)

var rotationLimitBytes int64 = defaultRotationLimitByte

type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
	LevelDebug Level = "debug"
)

type Visibility string

const (
	VisibilityInternal   Visibility = "internal"
	VisibilityUserFacing Visibility = "user"
)

type Options struct {
	StateRoot    string
	CrawlerID    string
	FileName     string
	RunID        string
	Command      string
	Version      string
	Commit       string
	Platform     string
	Debug        bool
	Verbosity    int
	JSONProgress bool
	Stderr       io.Writer
	Now          func() time.Time
}

type Run struct {
	stateRoot    string
	crawlerID    string
	fileName     string
	runID        string
	command      string
	version      string
	commit       string
	platform     string
	debug        bool
	verbosity    int
	jsonProgress bool
	stderr       io.Writer
	now          func() time.Time
	logPath      string

	mu       sync.Mutex
	finished bool
}

type WorldMustChange struct {
	Err     error
	Message string
	Remedy  string
}

func (e WorldMustChange) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return strings.TrimSpace(e.Message)
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "world must change"
}

func (e WorldMustChange) Unwrap() error {
	return e.Err
}

func NewRun(opts Options) (*Run, error) {
	run, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	if err := run.write(LevelInfo, "start", run.startMessage(), VisibilityInternal); err != nil {
		return nil, err
	}
	return run, nil
}

func (r *Run) RunID() string {
	if r == nil {
		return ""
	}
	return r.runID
}

func (r *Run) Path() string {
	if r == nil {
		return ""
	}
	return r.logPath
}

func (r *Run) Info(event, message string) error {
	return r.InfoWithVisibility(event, message, VisibilityInternal)
}

func (r *Run) InfoWithVisibility(event, message string, visibility Visibility) error {
	if r == nil {
		return nil
	}
	if err := guardPublicEvent(event); err != nil {
		return err
	}
	return r.write(LevelInfo, event, message, visibility)
}

func (r *Run) Warn(event, message string) error {
	return r.WarnWithVisibility(event, message, VisibilityInternal)
}

func (r *Run) WarnWithVisibility(event, message string, visibility Visibility) error {
	if r == nil {
		return nil
	}
	if err := guardPublicEvent(event); err != nil {
		return err
	}
	return r.write(LevelWarn, event, message, visibility)
}

func (r *Run) Debug(event, message string) error {
	return r.DebugWithVisibility(event, message, VisibilityInternal)
}

func (r *Run) DebugWithVisibility(event, message string, visibility Visibility) error {
	if r == nil || !r.debug {
		return nil
	}
	if err := guardPublicEvent(event); err != nil {
		return err
	}
	return r.write(LevelDebug, event, message, visibility)
}

func (r *Run) Error(event string, err error) error {
	return r.errorWithVisibility(event, err, VisibilityInternal, false)
}

func (r *Run) ErrorWithVisibility(event string, err error, visibility Visibility) error {
	return r.errorWithVisibility(event, err, visibility, true)
}

func (r *Run) errorWithVisibility(event string, err error, visibility Visibility, explicitVisibility bool) error {
	if r == nil {
		return nil
	}
	if guardErr := guardPublicEvent(event); guardErr != nil {
		return guardErr
	}
	if err == nil {
		err = errors.New("unknown error")
	}
	if _, ok := worldDetails(err); ok && !explicitVisibility {
		visibility = VisibilityUserFacing
	}
	message := "error=" + quoteValue(err.Error())
	if details, ok := worldDetails(err); ok {
		if details.remedy != "" {
			message += " remedy=" + quoteValue(details.remedy)
		}
		if details.message != "" {
			message = "error=" + quoteValue(details.message)
			if details.remedy != "" {
				message += " remedy=" + quoteValue(details.remedy)
			}
		}
	}
	return r.write(LevelError, event, message, visibility)
}

func (r *Run) Finish(err error) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return nil
	}
	r.finished = true
	r.mu.Unlock()

	if err == nil {
		return r.write(LevelInfo, "finish", "outcome=success", VisibilityInternal)
	}
	if logErr := r.Error("run_failed", err); logErr != nil {
		return logErr
	}
	return r.write(LevelInfo, "finish", "outcome=error", VisibilityInternal)
}

func guardPublicEvent(event string) error {
	if strings.TrimSpace(event) == "finish" {
		return errors.New("log finish event is reserved; use Run.Finish")
	}
	return nil
}

func (r *Run) Progress(opts ProgressOptions) *Progress {
	if r == nil {
		return nil
	}
	return newProgress(r, opts)
}

func (r *Run) startMessage() string {
	return fmt.Sprintf("version=%s commit=%s platform=%s", quoteValue(r.version), quoteValue(r.commit), quoteValue(r.platform))
}

func (r *Run) write(level Level, event, message string, visibility Visibility) error {
	if !validEvent(event) {
		return fmt.Errorf("invalid log event %q", event)
	}
	line := r.formatLine(r.now(), level, r.runID, r.command, event, messageWithVisibility(message, visibility))
	if err := guardLine(line); err != nil {
		if errors.Is(err, ErrUnsafeLogLine) {
			if logErr := r.writeRefusedLine(event); logErr != nil {
				return errors.Join(err, logErr)
			}
		}
		return err
	}
	return r.appendLogLine(line)
}

func (r *Run) writeRefusedLine(event string) error {
	message := messageWithVisibility("event="+quoteValue(event), VisibilityInternal)
	line := r.formatLine(r.now(), LevelWarn, r.runID, r.command, "log_line_refused", message)
	return r.appendLogLine(line)
}

func (r *Run) appendLogLine(line string) error {
	if err := guardLine(line); err != nil {
		return err
	}
	lock := lockForPath(r.logPath)
	lock.Lock()
	defer lock.Unlock()

	if err := os.MkdirAll(filepath.Dir(r.logPath), 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	lockFile, err := os.OpenFile(r.logPath+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open log lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return fmt.Errorf("lock log: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
	}()
	file, err := os.OpenFile(r.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	size := int64(0)
	if info, err := file.Stat(); err == nil {
		size = info.Size()
	}
	if size == 0 {
		header := r.formatLine(r.now(), LevelInfo, "-", "-", "grammar", Grammar)
		if err := guardLine(header); err != nil {
			_ = file.Close()
			return err
		}
		if _, err := io.WriteString(file, header+"\n"); err != nil {
			_ = file.Close()
			return fmt.Errorf("write log grammar: %w", err)
		}
	}
	if _, err := io.WriteString(file, line+"\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("write log line: %w", err)
	}
	if info, err := file.Stat(); err == nil {
		size = info.Size()
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close log: %w", err)
	}
	if size > rotationLimitBytes {
		if err := trimLog(r.logPath, rotationLimitBytes); err != nil {
			return err
		}
	}
	if r.verbosity > 0 {
		if _, err := fmt.Fprintln(r.stderr, line); err != nil {
			return fmt.Errorf("write verbose log line: %w", err)
		}
	}
	return nil
}

func (r *Run) formatLine(ts time.Time, level Level, runID, command, event, message string) string {
	return formatLine(ts, level, runID, command, event, message)
}

func normalizeOptions(opts Options) (*Run, error) {
	stateRoot := strings.TrimSpace(opts.StateRoot)
	if stateRoot == "" {
		return nil, errors.New("state root is required")
	}
	crawlerID := strings.TrimSpace(opts.CrawlerID)
	if !validPathSegment(crawlerID) {
		return nil, fmt.Errorf("invalid crawler id %q", opts.CrawlerID)
	}
	command := strings.TrimSpace(opts.Command)
	if !validField(command) || command == "-" {
		return nil, fmt.Errorf("invalid command %q", opts.Command)
	}
	runID := strings.TrimSpace(opts.RunID)
	if runID == "" {
		generated, err := generateRunID()
		if err != nil {
			return nil, err
		}
		runID = generated
	}
	if !validField(runID) || runID == "-" {
		return nil, fmt.Errorf("invalid run id %q", opts.RunID)
	}
	fileName := strings.TrimSpace(opts.FileName)
	if fileName == "" {
		fileName = currentLogName
	}
	if !validLogFileName(fileName) {
		return nil, fmt.Errorf("invalid log file name %q", opts.FileName)
	}
	version := defaultString(opts.Version, "unknown")
	commit := defaultString(opts.Commit, "unknown")
	platform := defaultString(opts.Platform, runtime.GOOS+"/"+runtime.GOARCH)
	verbosity := opts.Verbosity
	if verbosity < 0 {
		verbosity = 0
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	logPath := filepath.Join(stateRoot, crawlerID, "logs", fileName)
	return &Run{
		stateRoot:    stateRoot,
		crawlerID:    crawlerID,
		fileName:     fileName,
		runID:        runID,
		command:      command,
		version:      version,
		commit:       commit,
		platform:     platform,
		debug:        opts.Debug || verbosity >= 2,
		verbosity:    verbosity,
		jsonProgress: opts.JSONProgress,
		stderr:       stderr,
		now:          now,
		logPath:      logPath,
	}, nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func generateRunID() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func formatLine(ts time.Time, level Level, runID, command, event, message string) string {
	return fmt.Sprintf("%s %-5s %s %s %s: %s",
		ts.Format(logTimeLayout),
		formatLevel(level),
		runID,
		command,
		event,
		singleLine(message),
	)
}

func formatLevel(level Level) string {
	switch level {
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelDebug:
		return "DEBUG"
	default:
		return strings.ToUpper(string(level))
	}
}

func quoteValue(value string) string {
	value = singleLine(value)
	if value == "" {
		return strconv.Quote("")
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return strconv.Quote(value)
	}
	return value
}

func singleLine(value string) string {
	fields := strings.Fields(value)
	return strings.Join(fields, " ")
}

type worldErrorDetails struct {
	message string
	remedy  string
}

func worldDetails(err error) (worldErrorDetails, bool) {
	var world WorldMustChange
	if errors.As(err, &world) {
		return worldErrorDetails{
			message: strings.TrimSpace(world.Message),
			remedy:  strings.TrimSpace(world.Remedy),
		}, true
	}
	var worldPtr *WorldMustChange
	if errors.As(err, &worldPtr) && worldPtr != nil {
		return worldErrorDetails{
			message: strings.TrimSpace(worldPtr.Message),
			remedy:  strings.TrimSpace(worldPtr.Remedy),
		}, true
	}
	return worldErrorDetails{}, false
}

func messageWithVisibility(message string, visibility Visibility) string {
	message = singleLine(message)
	field := "visibility=" + string(normalizeVisibility(visibility))
	if message == "" {
		return field
	}
	return message + " " + field
}

func normalizeVisibility(visibility Visibility) Visibility {
	switch visibility {
	case VisibilityUserFacing:
		return VisibilityUserFacing
	default:
		return VisibilityInternal
	}
}

type ProgressOptions struct {
	Event string
	Unit  string
	Total int64
}

type Progress struct {
	run      *Run
	event    string
	unit     string
	total    int64
	started  time.Time
	lastLog  time.Time
	lastDone int64
	mu       sync.Mutex
}

type progressEvent struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	RunID     string `json:"run_id"`
	Command   string `json:"command"`
	Event     string `json:"event"`
	Message   string `json:"message"`
	Done      int64  `json:"done,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Unit      string `json:"unit,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

func newProgress(run *Run, opts ProgressOptions) *Progress {
	event := strings.TrimSpace(opts.Event)
	if event == "" {
		event = "progress"
	}
	if !validEvent(event) {
		event = "progress"
	}
	now := run.now()
	return &Progress{
		run:     run,
		event:   event,
		unit:    singleLine(opts.Unit),
		total:   opts.Total,
		started: now,
	}
}

func (p *Progress) Report(done int64, message string) error {
	if p == nil || p.run == nil {
		return nil
	}
	if done < 0 {
		done = 0
	}
	if p.total > 0 && done > p.total {
		done = p.total
	}
	now := p.run.now()
	event := p.eventPayload(now, done, message)
	if err := guardProgress(event); err != nil {
		if errors.Is(err, ErrUnsafeLogLine) {
			if logErr := p.run.writeRefusedLine(p.event); logErr != nil {
				return errors.Join(err, logErr)
			}
		}
		return err
	}
	if err := p.writeProgress(event); err != nil {
		return err
	}
	if p.shouldLog(now, done) {
		return p.run.Info(p.event, p.logMessage(event))
	}
	return nil
}

func (p *Progress) eventPayload(now time.Time, done int64, message string) progressEvent {
	return progressEvent{
		Type:      "progress",
		Timestamp: now.Format(time.RFC3339),
		RunID:     p.run.runID,
		Command:   p.run.command,
		Event:     p.event,
		Message:   singleLine(message),
		Done:      done,
		Total:     p.total,
		Unit:      p.unit,
		ElapsedMS: now.Sub(p.started).Milliseconds(),
	}
}

func (p *Progress) writeProgress(event progressEvent) error {
	if p.run.jsonProgress {
		return json.NewEncoder(p.run.stderr).Encode(event)
	}
	_, err := fmt.Fprintln(p.run.stderr, p.humanProgress(event))
	return err
}

func (p *Progress) humanProgress(event progressEvent) string {
	parts := []string{event.Message}
	if event.Total > 0 {
		done := strconv.FormatInt(event.Done, 10)
		total := strconv.FormatInt(event.Total, 10)
		if event.Unit != "" {
			parts = append(parts, done+"/"+total+" "+event.Unit)
		} else {
			parts = append(parts, done+"/"+total)
		}
	}
	parts = append(parts, "elapsed="+(time.Duration(event.ElapsedMS)*time.Millisecond).Round(time.Second).String())
	return strings.Join(parts, " ")
}

func (p *Progress) shouldLog(now time.Time, done int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastLog.IsZero() || now.Sub(p.lastLog) >= defaultProgressLogEvery || p.total > 0 && done == p.total && done != p.lastDone {
		p.lastLog = now
		p.lastDone = done
		return true
	}
	return false
}

func (p *Progress) logMessage(event progressEvent) string {
	parts := []string{event.Message}
	if event.Total > 0 {
		parts = append(parts, "done="+strconv.FormatInt(event.Done, 10), "total="+strconv.FormatInt(event.Total, 10))
	}
	if event.Unit != "" {
		parts = append(parts, "unit="+quoteValue(event.Unit))
	}
	parts = append(parts, "elapsed="+(time.Duration(event.ElapsedMS)*time.Millisecond).Round(time.Second).String())
	return strings.Join(parts, " ")
}
