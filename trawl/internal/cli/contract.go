package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const unknownFreshness = "—"

type StatusEnvelope struct {
	AppID           string       `json:"app_id"`
	State           string       `json:"state"`
	Summary         string       `json:"summary,omitempty"`
	Freshness       *Freshness   `json:"freshness,omitempty"`
	Counts          []Count      `json:"counts,omitempty"`
	Auth            SafeAuth     `json:"auth,omitempty"`
	DatabasePath    string       `json:"database_path,omitempty"`
	Databases       []Database   `json:"databases,omitempty"`
	LastSyncAt      string       `json:"last_sync_at,omitempty"`
	LastImportAt    string       `json:"last_import_at,omitempty"`
	LastSyncOutcome *SyncOutcome `json:"last_sync_outcome,omitempty"`
}

type Freshness struct {
	LastSync          string `json:"last_sync,omitempty"`
	Status            string `json:"status,omitempty"`
	AgeSeconds        int64  `json:"age_seconds,omitempty"`
	StaleAfterSeconds int64  `json:"stale_after_seconds,omitempty"`
}

type Count struct {
	ID    string     `json:"id"`
	Label string     `json:"label"`
	Value CountValue `json:"value"`
}

type CountValue struct {
	value any
}

func countValue(value any) CountValue {
	return CountValue{value: value}
}

func (v *CountValue) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	switch value := raw.(type) {
	case nil, string, bool:
		v.value = value
	case json.Number:
		if strings.ContainsAny(value.String(), ".eE") {
			parsed, err := strconv.ParseFloat(value.String(), 64)
			if err != nil {
				v.value = nil
				return nil
			}
			v.value = parsed
			return nil
		}
		parsed, err := strconv.ParseInt(value.String(), 10, 64)
		if err != nil {
			v.value = nil
			return nil
		}
		v.value = parsed
	default:
		v.value = nil
	}
	return nil
}

func (v CountValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.value)
}

func (v CountValue) text(id, label string) string {
	switch value := v.value.(type) {
	case nil:
		return unknownFreshness
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case int:
		return formatInteger(int64(value), id, label)
	case int64:
		return formatInteger(value, id, label)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return fmt.Sprint(value)
	}
}

type SafeAuth map[string]any

func (a *SafeAuth) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*a = nil
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	safe := SafeAuth{}
	for key, value := range raw {
		switch typed := value.(type) {
		case bool:
			safe[key] = typed
		case string:
			if key == "expires" {
				safe[key] = typed
			}
		case nil:
			if key == "expires" {
				safe[key] = nil
			}
		}
	}
	if len(safe) == 0 {
		*a = nil
		return nil
	}
	*a = safe
	return nil
}

func (a SafeAuth) boolKeys() []string {
	var keys []string
	for key, value := range a {
		if _, ok := value.(bool); ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

type Database struct {
	ID        string  `json:"id,omitempty"`
	Label     string  `json:"label,omitempty"`
	Kind      string  `json:"kind,omitempty"`
	Role      string  `json:"role,omitempty"`
	Path      string  `json:"path,omitempty"`
	Endpoint  string  `json:"endpoint,omitempty"`
	Archive   string  `json:"archive,omitempty"`
	IsPrimary bool    `json:"is_primary,omitempty"`
	Bytes     int64   `json:"bytes,omitempty"`
	Counts    []Count `json:"counts,omitempty"`
}

type SyncOutcome struct {
	State      string `json:"state,omitempty"`
	Message    string `json:"message,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

type DoctorEnvelope struct {
	Checks []DoctorCheck `json:"checks"`
}

type DoctorCheck struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Remedy  string `json:"remedy"`
}

func decodeContractJSON(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(out)
}

func normalizeStatus(source Source, status StatusEnvelope) StatusEnvelope {
	if status.AppID == "" {
		status.AppID = source.ID
	}
	if status.State == "" {
		status.State = "error"
	}
	return status
}

func errorStatus(source Source, summary string) StatusEnvelope {
	return StatusEnvelope{
		AppID:   source.ID,
		State:   "error",
		Summary: summary,
	}
}

func statusFailed(status StatusEnvelope) bool {
	return status.State == "error" || status.State == "missing"
}

func checkFailed(check DoctorCheck) bool {
	return check.State == "fail" || check.State == "error" || check.State == "missing"
}

func formatInteger(value int64, id, label string) string {
	name := strings.ToLower(strings.TrimSpace(label))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(id))
	}
	if name == "since" || strings.Contains(name, "year") {
		return strconv.FormatInt(value, 10)
	}
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	digits := strconv.FormatInt(value, 10)
	var chunks []string
	for len(digits) > 3 {
		chunks = append([]string{digits[len(digits)-3:]}, chunks...)
		digits = digits[:len(digits)-3]
	}
	chunks = append([]string{digits}, chunks...)
	return sign + strings.Join(chunks, ",")
}

func freshnessText(status StatusEnvelope, now time.Time) string {
	if status.Freshness != nil {
		if status.Freshness.LastSync != "" {
			if parsed, err := time.Parse(time.RFC3339, status.Freshness.LastSync); err == nil {
				return humanDuration(now.Sub(parsed))
			}
		}
		if status.Freshness.AgeSeconds > 0 {
			return humanDuration(time.Duration(status.Freshness.AgeSeconds) * time.Second)
		}
	}
	if status.LastSyncAt != "" {
		if parsed, err := time.Parse(time.RFC3339, status.LastSyncAt); err == nil {
			return humanDuration(now.Sub(parsed))
		}
	}
	return unknownFreshness
}

func humanDuration(duration time.Duration) string {
	if duration < time.Minute {
		return "just now"
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm ago", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(duration.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(duration.Hours()/24))
}
