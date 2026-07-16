package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const unknownFreshness = "not synced yet"

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

func statusState(status *federationv1.SourceStatus) string {
	state := strings.TrimSpace(status.GetState())
	if state == "" {
		return "error"
	}
	return state
}

func statusSummary(status *federationv1.SourceStatus) string {
	if summary := strings.TrimSpace(status.GetSummary()); summary != "" {
		return summary
	}
	if len(status.GetErrors()) > 0 {
		return strings.TrimSpace(status.GetErrors()[0])
	}
	if statusState(status) == "missing" {
		return "Not synced yet."
	}
	return ""
}

func statusFailed(status *federationv1.SourceStatus) bool {
	state := statusState(status)
	return state == "error" || state == "missing"
}

func freshnessText(status *federationv1.SourceStatus, now time.Time) string {
	if value := strings.TrimSpace(status.GetLastSyncRfc3339()); value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return humanDuration(now.Sub(parsed))
		}
	}
	if status.GetFreshness().GetAgeSeconds() > 0 {
		return humanDuration(time.Duration(status.GetFreshness().GetAgeSeconds()) * time.Second)
	}
	if value := strings.TrimSpace(status.GetLastImportRfc3339()); value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
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
