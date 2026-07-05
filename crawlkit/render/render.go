// Package render writes shared human output for crawler status and doctor
// commands.
package render

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
)

type CheckState string

const (
	CheckOK      CheckState = "ok"
	CheckEmpty   CheckState = "empty"
	CheckMissing CheckState = "missing"
	CheckFail    CheckState = "fail"
)

type StatusState string

const (
	StatusOK      StatusState = "ok"
	StatusStale   StatusState = "stale"
	StatusEmpty   StatusState = "empty"
	StatusError   StatusState = "error"
	StatusMissing StatusState = "missing"
	StatusUnknown StatusState = "unknown"
)

type Check struct {
	Name    string
	State   CheckState
	Message string
	Remedy  string
}

type Status struct {
	State     StatusState
	Summary   string
	Sections  []Section
	Freshness *Freshness
	Log       LogTail
	Warnings  []string
	Errors    []string
}

type Section struct {
	Title  string
	Fields []Field
}

type Field struct {
	Label string
	Value string
}

type Freshness struct {
	LastSync string
	Label    string // defaults to "Last sync"; import-only crawlers say "Last import"
	State    string
}

type LogTail struct {
	LastRun         *cklog.RunSummary
	MostRecentError *cklog.Line
	Errors          []string
}

type DoctorLogTail struct {
	LastRun         *DoctorLogEvent  `json:"last_run,omitempty"`
	MostRecentError *DoctorLogEvent  `json:"most_recent_error,omitempty"`
	LogProblems     []DoctorLogEvent `json:"log_problems,omitempty"`
}

type DoctorLogEvent struct {
	WhatHappened string `json:"what_happened"`
	When         string `json:"when,omitempty"`
	Remedy       string `json:"remedy,omitempty"`
}

var logFieldPattern = regexp.MustCompile(`\b([a-z][a-z0-9_]*)=("(?:\\.|[^"])*"|[^ ]+)`)

func WriteDoctor(w io.Writer, checks []Check, tail LogTail) error {
	if err := WriteChecks(w, checks); err != nil {
		return err
	}
	return WriteLogTail(w, doctorLogTail(tail))
}

func WriteChecks(w io.Writer, checks []Check) error {
	if _, err := io.WriteString(w, "Doctor checks:\n"); err != nil {
		return err
	}
	for _, check := range checks {
		if _, err := fmt.Fprintf(w, "  %s: %s", displayCheckName(check.Name), check.State); err != nil {
			return err
		}
		if message := strings.TrimSpace(check.Message); message != "" {
			if _, err := fmt.Fprintf(w, " - %s", message); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		if remedy := strings.TrimSpace(check.Remedy); remedy != "" {
			if _, err := fmt.Fprintf(w, "    Remedy: %s\n", remedy); err != nil {
				return err
			}
		}
	}
	return nil
}

func displayCheckName(name string) string {
	return displayCode(name)
}

func displayRunOutcome(outcome string) string {
	switch strings.TrimSpace(outcome) {
	case "success":
		return "succeeded"
	case "failure", "error":
		// The log reader canonicalizes every failed run to "error"
		// (reader.go finishOutcome); "failure" stays for older lines.
		return "failed"
	default:
		return displayCode(outcome)
	}
}

func displayLogEvent(event string) string {
	if strings.TrimSpace(event) == "run_failed" {
		return "failed"
	}
	return displayCode(event)
}

func displayCode(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(value), "_", " ")), " ")
}

func WriteStatus(w io.Writer, status Status) error {
	state := status.State
	if state == "" {
		state = StatusUnknown
	}
	if _, err := fmt.Fprintf(w, "Status: %s\n%s\n", state, strings.TrimSpace(status.Summary)); err != nil {
		return err
	}
	for _, section := range status.Sections {
		if err := writeSection(w, section); err != nil {
			return err
		}
	}
	if status.Freshness != nil {
		if err := writeFreshness(w, *status.Freshness); err != nil {
			return err
		}
	}
	if err := writeMessages(w, "Warnings", status.Warnings); err != nil {
		return err
	}
	if err := writeMessages(w, "Errors", status.Errors); err != nil {
		return err
	}
	return WriteLogTail(w, status.Log)
}

func WriteLogTail(w io.Writer, tail LogTail) error {
	if tail.LastRun == nil && tail.MostRecentError == nil && len(tail.Errors) == 0 {
		return nil
	}
	if _, err := io.WriteString(w, "\nRecent log:\n"); err != nil {
		return err
	}
	if tail.LastRun != nil {
		if err := writeLastRun(w, *tail.LastRun); err != nil {
			return err
		}
	}
	if tail.MostRecentError != nil {
		if err := writeRecentError(w, *tail.MostRecentError); err != nil {
			return err
		}
	}
	return writeMessages(w, "Log errors", tail.Errors)
}

func doctorLogTail(tail LogTail) LogTail {
	if tail.MostRecentError != nil && !cklog.IsUserFacingError(*tail.MostRecentError) {
		tail.MostRecentError = nil
	}
	return tail
}

func DoctorLogTailOutput(tail LogTail) *DoctorLogTail {
	tail = doctorLogTail(tail)
	out := &DoctorLogTail{}
	if tail.LastRun != nil {
		out.LastRun = doctorLastRunOutput(*tail.LastRun)
	}
	if tail.MostRecentError != nil {
		out.MostRecentError = doctorRecentErrorOutput(*tail.MostRecentError)
	}
	for _, err := range tail.Errors {
		if whatHappened := strings.TrimSpace(err); whatHappened != "" {
			out.LogProblems = append(out.LogProblems, DoctorLogEvent{WhatHappened: whatHappened})
		}
	}
	if out.LastRun == nil && out.MostRecentError == nil && len(out.LogProblems) == 0 {
		return nil
	}
	return out
}

func doctorLastRunOutput(run cklog.RunSummary) *DoctorLogEvent {
	whatHappened := strings.TrimSpace(run.Command)
	if outcome := strings.TrimSpace(run.Outcome); outcome != "" {
		if whatHappened == "" {
			whatHappened = "command ended with " + outcome
		} else {
			whatHappened += " ended with " + outcome
		}
	}
	if whatHappened == "" {
		whatHappened = "command ran"
	}
	return &DoctorLogEvent{
		WhatHappened: whatHappened,
		When:         jsonTime(firstTime(run.FinishedAt, run.StartedAt)),
	}
}

func doctorRecentErrorOutput(line cklog.Line) *DoctorLogEvent {
	message, remedy := logErrorMessage(line.Message)
	if message == "" {
		message = "error"
	}
	return &DoctorLogEvent{
		WhatHappened: message,
		When:         jsonTime(line.Timestamp),
		Remedy:       remedy,
	}
}

func writeSection(w io.Writer, section Section) error {
	title := strings.TrimSpace(section.Title)
	if title == "" || len(section.Fields) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n%s:\n", title); err != nil {
		return err
	}
	for _, field := range section.Fields {
		label := strings.TrimSpace(field.Label)
		if label == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "  %s: %s\n", label, emptyDash(field.Value)); err != nil {
			return err
		}
	}
	return nil
}

func writeFreshness(w io.Writer, freshness Freshness) error {
	var fields []Field
	if freshness.LastSync != "" {
		label := freshness.Label
		if label == "" {
			label = "Last sync"
		}
		fields = append(fields, Field{Label: label, Value: freshness.LastSync})
	}
	if freshness.State != "" {
		fields = append(fields, Field{Label: "State", Value: freshness.State})
	}
	return writeSection(w, Section{Title: "Freshness", Fields: fields})
}

func writeLastRun(w io.Writer, run cklog.RunSummary) error {
	command := emptyDash(run.Command)
	outcome := emptyDash(displayRunOutcome(run.Outcome))
	if _, err := fmt.Fprintf(w, "  Last run: %s %s", command, outcome); err != nil {
		return err
	}
	if when := firstTime(run.FinishedAt, run.StartedAt); !when.IsZero() {
		if _, err := fmt.Fprintf(w, " at %s", formatTime(when)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func writeRecentError(w io.Writer, line cklog.Line) error {
	message, remedy := logErrorMessage(line.Message)
	event := strings.TrimSpace(strings.Join(nonEmpty(line.Command, displayLogEvent(line.Event)), " "))
	if event == "" {
		event = "error"
	}
	if _, err := fmt.Fprintf(w, "  Most recent error: %s", event); err != nil {
		return err
	}
	if message != "" {
		if _, err := fmt.Fprintf(w, ": %s", message); err != nil {
			return err
		}
	}
	if when := formatTime(line.Timestamp); when != "" {
		if _, err := fmt.Fprintf(w, " (at %s)", when); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	if remedy != "" {
		if _, err := fmt.Fprintf(w, "    Remedy: %s\n", remedy); err != nil {
			return err
		}
	}
	return nil
}

func writeMessages(w io.Writer, title string, values []string) error {
	if len(values) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n%s:\n", title); err != nil {
		return err
	}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			if _, err := fmt.Fprintf(w, "  - %s\n", value); err != nil {
				return err
			}
		}
	}
	return nil
}

func logErrorMessage(message string) (string, string) {
	fields := parseLogFields(message)
	text := firstNonEmpty(fields["error"], fields["message"])
	if text == "" {
		text = stripLogFields(message, "event", "last_event", "run_id", "visibility")
	}
	return text, fields["remedy"]
}

func stripLogFields(message string, names ...string) string {
	remove := make(map[string]bool, len(names))
	for _, name := range names {
		remove[name] = true
	}
	stripped := logFieldPattern.ReplaceAllStringFunc(message, func(match string) string {
		parts := logFieldPattern.FindStringSubmatch(match)
		if len(parts) == 3 && remove[parts[1]] {
			return ""
		}
		return match
	})
	return strings.Join(strings.Fields(stripped), " ")
}

func parseLogFields(message string) map[string]string {
	fields := make(map[string]string)
	for _, match := range logFieldPattern.FindAllStringSubmatch(message, -1) {
		value := match[2]
		if strings.HasPrefix(value, `"`) {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
		}
		fields[match[1]] = value
	}
	return fields
}

// formatTime is the human-mode time: short local. JSON keeps full RFC3339
// via jsonTime — the two surfaces must never share a formatter.
func formatTime(value time.Time) string {
	return ShortLocalTime(value)
}

func jsonTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func firstTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
