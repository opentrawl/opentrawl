package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRunVersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "dev" {
		t.Fatalf("version stdout = %q", stdout.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"export"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export should be unknown stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr missing unknown command:\n%s", stderr.String())
	}
}

func TestRunUnknownFlagJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--json", "--not-a-real-flag"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("unknown flag should fail stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("JSON error wrote stderr: %s", stderr.String())
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Remedy  string `json:"remedy"`
		} `json:"error"`
	}
	assertSingleJSONDocument(t, stdout.String(), &payload)
	if payload.Error.Code != "usage" || payload.Error.Message == "" || payload.Error.Remedy == "" {
		t.Fatalf("error payload = %#v", payload)
	}
}

func TestRunCommandUsageErrorsJSON(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "search", args: []string{"search", "--json"}},
		{name: "who", args: []string{"who", " ", "--json"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("JSON error wrote stderr: %s", stderr.String())
			}
			var payload struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
					Remedy  string `json:"remedy"`
				} `json:"error"`
			}
			assertSingleJSONDocument(t, stdout.String(), &payload)
			if payload.Error.Code != "usage" || payload.Error.Message == "" || payload.Error.Remedy == "" {
				t.Fatalf("error payload = %#v", payload)
			}
		})
	}
}

func TestRunHumanUsageErrorStrings(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		stderr string
	}{
		{
			name:   "search",
			args:   []string{"search"},
			stderr: "search requires a query or at least one filter (--who, --after, --before)\n",
		},
		{
			name:   "who",
			args:   []string{"who", " "},
			stderr: "who requires a name fragment\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("human usage wrote stdout: %s", stdout.String())
			}
			if stderr.String() != tc.stderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.stderr)
			}
		})
	}
}

func assertSingleJSONDocument(t *testing.T, data string, out any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(data))
	if err := dec.Decode(out); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, data)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON output had trailing data: %v\n%s", err, data)
	}
}
