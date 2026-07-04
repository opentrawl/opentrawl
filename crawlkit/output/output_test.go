package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestResolveJSONFlagWins(t *testing.T) {
	got, err := Resolve("text", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != JSON {
		t.Fatalf("format = %q", got)
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, JSON, "status", map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"ok": true`)) {
		t.Fatalf("json output = %s", buf.String())
	}
}

func TestWriteLogRejectsAmbiguousLabels(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, Log, "status.ok-1", map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("status.ok-1=")) {
		t.Fatalf("log output = %s", buf.String())
	}
	for _, label := range []string{"bad\nlabel", "bad=label"} {
		if err := Write(&buf, Log, label, map[string]any{"ok": true}); !IsUsage(err) {
			t.Fatalf("Write label %q error = %v, want usage error", label, err)
		}
	}
}

func TestUsageError(t *testing.T) {
	_, err := Resolve("xml", false)
	if !IsUsage(err) {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestWriteErrorContractEnvelope(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteError(&buf, ErrorBody{
		Code:    "usage",
		Message: "search --limit must be between 1 and 200",
		Remedy:  "run gogcrawl help",
		Fields: map[string]any{
			"candidates":      []string{},
			"candidate_total": 0,
			"did_you_mean":    []string{"alice@example.com"},
			"hint":            "",
		},
	}); err != nil {
		t.Fatal(err)
	}
	var payload ErrorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("error JSON = %s err=%v", buf.String(), err)
	}
	if payload.Error.Code != "usage" || payload.Error.Message == "" || payload.Error.Remedy == "" {
		t.Fatalf("error envelope = %#v", payload)
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["error"]["candidates"]; ok {
		t.Fatalf("empty candidates should be omitted: %s", buf.String())
	}
	if _, ok := raw["error"]["candidate_total"]; ok {
		t.Fatalf("zero candidate_total should be omitted: %s", buf.String())
	}
	if _, ok := raw["error"]["hint"]; ok {
		t.Fatalf("empty hint should be omitted: %s", buf.String())
	}
	if _, ok := raw["error"]["did_you_mean"]; !ok {
		t.Fatalf("non-empty did_you_mean should be kept: %s", buf.String())
	}
}

func TestRenderedErrorKeepsUnderlyingError(t *testing.T) {
	underlying := UsageError{Err: errors.New("bad flag")}
	err := Rendered(underlying)
	if !IsRendered(err) {
		t.Fatalf("rendered marker missing: %v", err)
	}
	if !IsUsage(err) {
		t.Fatalf("underlying usage error missing: %v", err)
	}
}
