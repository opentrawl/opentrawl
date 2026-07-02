package main

import (
	"bytes"
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
	code := run([]string{"sync"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("sync should be unknown stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr missing unknown command:\n%s", stderr.String())
	}
}
