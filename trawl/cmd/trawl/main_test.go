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
	code := run([]string{"export"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export should be unknown stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if stderr.String() != "Unknown command \"export\".\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
