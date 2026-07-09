package archive

import (
	"encoding/json"
	"time"
)

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func mustJSONList(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func decodeJSON(value string, dst any) error {
	if value == "" {
		value = "{}"
	}
	return json.Unmarshal([]byte(value), dst)
}

func decodeJSONList(value string, dst any) error {
	if value == "" {
		value = "[]"
	}
	return json.Unmarshal([]byte(value), dst)
}

func timeText(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
