package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const unknownFreshness = "not synced yet"

type StatusEnvelope struct {
	AppID             string                     `json:"app_id"`
	Surface           string                     `json:"surface,omitempty"`
	State             string                     `json:"state"`
	Summary           string                     `json:"summary,omitempty"`
	Freshness         *Freshness                 `json:"freshness,omitempty"`
	Counts            []Count                    `json:"counts,omitempty"`
	DatabasePath      string                     `json:"database_path,omitempty"`
	Databases         []Database                 `json:"databases,omitempty"`
	LastSyncAt        string                     `json:"last_sync_at,omitempty"`
	LastImportAt      string                     `json:"last_import_at,omitempty"`
	SetupRequirements []control.SetupRequirement `json:"setup_requirements,omitempty"`
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
		return render.FormatCount(int64(value), id, label)
	case int64:
		return render.FormatCount(value, id, label)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return fmt.Sprint(value)
	}
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
	if status.Surface == "" {
		status.Surface = sourceHumanName(source)
	}
	status.State = strings.TrimSpace(status.State)
	if status.State == "" {
		status.State = "error"
	}
	switch status.State {
	case "ok":
		status.Summary = "Recently synced."
	case "missing", "error":
		status.Summary = "Not synced yet."
	}
	return status
}

func errorStatus(source Source, summary string) StatusEnvelope {
	return StatusEnvelope{
		AppID:   source.ID,
		Surface: sourceHumanName(source),
		State:   "error",
		Summary: summary,
	}
}

func statusFailed(status StatusEnvelope) bool {
	return status.State == "error" || status.State == "missing"
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
	if status.LastImportAt != "" {
		if parsed, err := time.Parse(time.RFC3339, status.LastImportAt); err == nil {
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
