package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const jsonIndent = "  "

func renderOpenPayload(w io.Writer, value any, fallbackRef string) error {
	object, ok := value.(map[string]any)
	if !ok {
		return renderJSONValue(w, value, 0)
	}
	if render, ok := openTranscriptRenderer(object, fallbackRef); ok {
		return render(w)
	}
	if render, ok := openMailRenderer(object, fallbackRef); ok {
		return render(w)
	}
	if render, ok := openCalendarRenderer(object, fallbackRef); ok {
		return render(w)
	}
	return renderJSONValue(w, value, 0)
}

type openRenderer func(io.Writer) error

type transcriptLine struct {
	Stamp  string
	Who    string
	Text   string
	Target bool

	parsed time.Time
	timeOK bool
}

func openTranscriptRenderer(object map[string]any, fallbackRef string) (openRenderer, bool) {
	chat, hasChat := mapValue(object["chat"])
	message, hasMessage := mapValue(object["message"])
	context, hasContext := sliceValue(object["context"])
	if !hasChat || (!hasMessage && !hasContext) {
		return nil, false
	}

	title := firstNonEmpty(stringField(chat, "name", "title"), stringField(message, "where"), "conversation")
	ref := firstNonEmpty(stringField(object, "ref"), stringField(message, "ref"), fallbackRef)
	targetRef := stringField(message, "ref")

	lines := make([]transcriptLine, 0, len(context)+1)
	targetSeen := false
	for _, item := range context {
		row, ok := mapValue(item)
		if !ok {
			continue
		}
		line := transcriptLineFromMap(row)
		if !line.Target && targetRef != "" && stringField(row, "ref") == targetRef {
			line.Target = true
		}
		targetSeen = targetSeen || line.Target
		lines = append(lines, line)
	}
	if len(lines) == 0 && hasMessage {
		line := transcriptLineFromMap(message)
		line.Target = true
		targetSeen = true
		lines = append(lines, line)
	}
	if !targetSeen && len(lines) == 1 {
		lines[0].Target = true
	}

	return func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "%s\n\n", transcriptHeader(title, message, lines)); err != nil {
			return err
		}
		showSeparators := transcriptSpansDays(lines)
		previousDay := ""
		for _, line := range lines {
			if showSeparators {
				if err := writeTranscriptDaySeparator(w, line, &previousDay); err != nil {
					return err
				}
			}
			if err := writeTranscriptLine(w, line); err != nil {
				return err
			}
		}
		return writeOpenRef(w, ref)
	}, true
}

func transcriptLineFromMap(object map[string]any) transcriptLine {
	stamp := stringField(object, "time", "date", "sent_at", "created_at", "start")
	parsed, ok := parseOpenTime(stamp)
	return transcriptLine{
		Stamp:  stamp,
		Who:    senderName(object),
		Text:   messageText(object),
		Target: boolField(object, "target"),
		parsed: parsed,
		timeOK: ok,
	}
}

func transcriptHeader(title string, message map[string]any, lines []transcriptLine) string {
	openedAt, ok := transcriptOpenedAt(message, lines)
	if !ok {
		return title
	}
	return title + " — " + formatTranscriptDay(openedAt)
}

func transcriptOpenedAt(message map[string]any, lines []transcriptLine) (time.Time, bool) {
	if message != nil {
		stamp := stringField(message, "time", "date", "sent_at", "created_at", "start")
		if parsed, ok := parseOpenTime(stamp); ok {
			return parsed, true
		}
	}
	for _, line := range lines {
		if line.Target && line.timeOK {
			return line.parsed, true
		}
	}
	for _, line := range lines {
		if line.timeOK {
			return line.parsed, true
		}
	}
	return time.Time{}, false
}

func writeTranscriptDaySeparator(w io.Writer, line transcriptLine, previousDay *string) error {
	if !line.timeOK {
		return nil
	}
	day := line.parsed.Format("2006-01-02")
	if *previousDay == "" {
		*previousDay = day
		return nil
	}
	if day == *previousDay {
		return nil
	}
	*previousDay = day
	_, err := fmt.Fprintf(w, "— %s —\n", formatTranscriptDay(line.parsed))
	return err
}

func writeTranscriptLine(w io.Writer, line transcriptLine) error {
	prefix := ""
	if line.Target {
		prefix = "▶ "
	}
	speaker := ""
	if line.Who != "" {
		speaker = line.Who + ": "
	}
	_, err := fmt.Fprintf(w, "%s%s  %s%s\n", prefix, transcriptStamp(line), speaker, line.Text)
	return err
}

// senderName digs the display name out of flat or nested sender
// shapes; nothing humane means no name, never a placeholder.
func senderName(object map[string]any) string {
	if name := stringField(object, "who", "from", "author"); name != "" {
		return name
	}
	if sender, ok := object["sender"].(map[string]any); ok {
		return stringField(sender, "display_name", "name", "who")
	}
	return stringField(object, "sender")
}

func transcriptSpansDays(lines []transcriptLine) bool {
	var first string
	for _, line := range lines {
		if !line.timeOK {
			continue
		}
		day := line.parsed.Format("2006-01-02")
		if first == "" {
			first = day
			continue
		}
		if day != first {
			return true
		}
	}
	return false
}

func formatTranscriptDay(value time.Time) string {
	return value.Format("Mon 2 Jan 2006")
}

func transcriptStamp(line transcriptLine) string {
	if !line.timeOK {
		return firstNonEmpty(line.Stamp, unknownFreshness)
	}
	return line.parsed.Format("15:04")
}

func messageText(object map[string]any) string {
	text := collapseWhitespace(firstNonEmpty(stringField(object, "text", "body", "snippet", "summary")))
	if text == "" && boolField(object, "has_attachments") {
		return "[attachment]"
	}
	if text == "" {
		return unknownFreshness
	}
	if boolField(object, "has_attachments") {
		return text + " [attachment]"
	}
	return text
}

func openMailRenderer(object map[string]any, fallbackRef string) (openRenderer, bool) {
	headers, ok := mapValue(object["headers"])
	if !ok {
		return nil, false
	}
	subject := firstNonEmpty(stringField(headers, "subject"), stringField(object, "subject"), "(no subject)")
	from := mailAddress(headers, "from_name", "from_address", "from")
	to := firstNonEmpty(joinStringField(headers, "to_address", "to"), joinStringField(object, "to"))
	cc := firstNonEmpty(joinStringField(headers, "cc_address", "cc"), joinStringField(object, "cc"))
	sent := formatOpenStamp(stringField(object, "time", "date", "sent_at"), true)
	body := strings.TrimSpace(stringField(object, "body", "text"))
	ref := firstNonEmpty(stringField(object, "ref"), fallbackRef)
	attachmentCount := len(sliceField(object, "attachments"))

	return func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "%s\n", subject); err != nil {
			return err
		}
		if from != "" || to != "" {
			if _, err := fmt.Fprintf(w, "From %s to %s.\n", firstNonEmpty(from, "unknown sender"), firstNonEmpty(to, "unknown recipient")); err != nil {
				return err
			}
		}
		if cc != "" {
			if _, err := fmt.Fprintf(w, "Cc %s.\n", cc); err != nil {
				return err
			}
		}
		if sent != "" {
			if _, err := fmt.Fprintf(w, "Sent %s.\n", sent); err != nil {
				return err
			}
		}
		if attachmentCount > 0 {
			label := "attachment"
			if attachmentCount != 1 {
				label = "attachments"
			}
			if _, err := fmt.Fprintf(w, "%d %s.\n", attachmentCount, label); err != nil {
				return err
			}
		}
		if body != "" {
			if _, err := fmt.Fprintf(w, "\n%s\n", body); err != nil {
				return err
			}
		}
		return writeOpenRef(w, ref)
	}, true
}

func openCalendarRenderer(object map[string]any, fallbackRef string) (openRenderer, bool) {
	start := stringField(object, "start", "starts_at", "time")
	end := stringField(object, "end", "ends_at")
	if start == "" || end == "" {
		return nil, false
	}
	title := firstNonEmpty(stringField(object, "title", "summary", "name"), "event")
	calendar := namedValue(object["calendar"])
	location := locationText(object["location"])
	organizer := personText(object["organizer"])
	attendees := attendeeTexts(sliceField(object, "attendees"))
	description := strings.TrimSpace(stringField(object, "description", "body"))
	ref := firstNonEmpty(stringField(object, "ref"), fallbackRef)

	return func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "%s\n", title); err != nil {
			return err
		}
		when := formatOpenStamp(start, true) + " to " + formatOpenStamp(end, true)
		if calendar != "" {
			when += " on " + calendar
		}
		if _, err := fmt.Fprintf(w, "%s.\n", when); err != nil {
			return err
		}
		if location != "" {
			if _, err := fmt.Fprintf(w, "Location: %s.\n", location); err != nil {
				return err
			}
		}
		if organizer != "" {
			if _, err := fmt.Fprintf(w, "Organiser: %s.\n", organizer); err != nil {
				return err
			}
		}
		if len(attendees) > 0 {
			if _, err := fmt.Fprintf(w, "Attendees: %s.\n", strings.Join(attendees, ", ")); err != nil {
				return err
			}
		}
		if description != "" {
			if _, err := fmt.Fprintf(w, "\n%s\n", description); err != nil {
				return err
			}
		}
		return writeOpenRef(w, ref)
	}, true
}

func attendeeTexts(items []any) []string {
	const attendeeLimit = 6
	out := make([]string, 0, min(len(items), attendeeLimit)+1)
	for _, item := range items {
		object, ok := mapValue(item)
		if !ok {
			continue
		}
		name := personText(object)
		if name == "" {
			continue
		}
		status := stringField(object, "rsvp_status", "status")
		if status != "" {
			name += " (" + status + ")"
		}
		out = append(out, name)
		if len(out) == attendeeLimit {
			break
		}
	}
	if extra := len(items) - len(out); extra > 0 && len(out) == attendeeLimit {
		out = append(out, fmt.Sprintf("%d more", extra))
	}
	return out
}

func personText(value any) string {
	object, ok := mapValue(value)
	if !ok {
		return openString(value)
	}
	return firstNonEmpty(
		stringField(object, "display_name", "name"),
		stringField(object, "email"),
		stringField(object, "phone_number", "phone", "address"),
	)
}

func mailAddress(object map[string]any, nameKey, addressKey, fallbackKey string) string {
	name := stringField(object, nameKey)
	address := stringField(object, addressKey)
	if name != "" && address != "" {
		return name + " <" + address + ">"
	}
	return firstNonEmpty(name, address, joinStringField(object, fallbackKey))
}

func locationText(value any) string {
	object, ok := mapValue(value)
	if !ok {
		return openString(value)
	}
	return strings.Join(orderedNonEmpty(stringField(object, "title", "name"), stringField(object, "address")), ", ")
}

func namedValue(value any) string {
	object, ok := mapValue(value)
	if !ok {
		return openString(value)
	}
	return firstNonEmpty(stringField(object, "title", "name", "display_name"))
}

func writeOpenRef(w io.Writer, ref string) error {
	if ref == "" {
		return nil
	}
	_, err := fmt.Fprintf(w, "\nref: %s\n", ref)
	return err
}

func formatOpenStamp(value string, includeDate bool) string {
	parsed, ok := parseOpenTime(value)
	if !ok {
		return value
	}
	if includeDate && len(value) == len("2006-01-02") {
		return parsed.Format("2006-01-02")
	}
	if includeDate {
		return parsed.Format("2006-01-02 15:04")
	}
	return parsed.Format("15:04")
}

func parseOpenTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return parsed, true
	}
	parsed, err = time.Parse("2006-01-02", value)
	if err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func stringField(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := openString(object[key]); value != "" {
			return value
		}
	}
	return ""
}

func joinStringField(object map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := object[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []any:
			parts := make([]string, 0, len(typed))
			for _, item := range typed {
				if text := openString(item); text != "" {
					parts = append(parts, text)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, ", ")
			}
		default:
			if text := openString(typed); text != "" {
				return text
			}
		}
	}
	return ""
}

func boolField(object map[string]any, key string) bool {
	value, _ := object[key].(bool)
	return value
}

func mapValue(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func sliceField(object map[string]any, key string) []any {
	items, _ := sliceValue(object[key])
	return items
}

func sliceValue(value any) ([]any, bool) {
	items, ok := value.([]any)
	return items, ok
}

func openString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case nil:
		return ""
	default:
		return ""
	}
}

func orderedNonEmpty(values ...string) []string {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func renderJSONValue(w io.Writer, value any, indent int) error {
	prefix := strings.Repeat(jsonIndent, indent)
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if isJSONScalar(typed[key]) {
				if _, err := fmt.Fprintf(w, "%s%s: %s\n", prefix, key, jsonScalarText(typed[key])); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(w, "%s%s:\n", prefix, key); err != nil {
				return err
			}
			if err := renderJSONValue(w, typed[key], indent+1); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if isJSONScalar(item) {
				if _, err := fmt.Fprintf(w, "%s- %s\n", prefix, jsonScalarText(item)); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(w, "%s-\n", prefix); err != nil {
				return err
			}
			if err := renderJSONValue(w, item, indent+1); err != nil {
				return err
			}
		}
	default:
		_, err := fmt.Fprintf(w, "%s%s\n", prefix, jsonScalarText(typed))
		return err
	}
	return nil
}

func isJSONScalar(value any) bool {
	switch value.(type) {
	case nil, string, bool, json.Number, float64:
		return true
	default:
		return false
	}
}

func jsonScalarText(value any) string {
	switch typed := value.(type) {
	case nil:
		return unknownFreshness
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprint(typed)
	default:
		return fmt.Sprint(typed)
	}
}
