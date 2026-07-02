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
	State    string
}

type LogTail struct {
	LastRun         *cklog.RunSummary
	MostRecentError *cklog.Line
	Errors          []string
}

var logFieldPattern = regexp.MustCompile(`\b([a-z][a-z0-9_]*)=("(?:\\.|[^"])*"|[^ ]+)`)

func WriteDoctor(w io.Writer, checks []Check, tail LogTail) error {
	if err := WriteChecks(w, checks); err != nil {
		return err
	}
	return WriteLogTail(w, tail)
}

func WriteChecks(w io.Writer, checks []Check) error {
	if _, err := io.WriteString(w, "Doctor checks:\n"); err != nil {
		return err
	}
	for _, check := range checks {
		if _, err := fmt.Fprintf(w, "  %s: %s", strings.TrimSpace(check.Name), check.State); err != nil {
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
		fields = append(fields, Field{Label: "Last sync", Value: freshness.LastSync})
	}
	if freshness.State != "" {
		fields = append(fields, Field{Label: "State", Value: freshness.State})
	}
	return writeSection(w, Section{Title: "Freshness", Fields: fields})
}

func writeLastRun(w io.Writer, run cklog.RunSummary) error {
	command := emptyDash(run.Command)
	outcome := emptyDash(run.Outcome)
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
	event := strings.TrimSpace(strings.Join(nonEmpty(line.Command, line.Event), " "))
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
		text = strings.TrimSpace(message)
	}
	return text, fields["remedy"]
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

func formatTime(value time.Time) string {
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
