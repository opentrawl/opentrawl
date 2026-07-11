package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestIssueHelpAlignsEveryUsageCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := execute([]string{"issue", "--help"}, strings.NewReader(""), &stdout, &stderr)
	if _, ok := err.(helpShown); !ok {
		t.Fatalf("execute returned %v, want helpShown", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	for _, command := range []string{
		"  linear issue <ISSUE>",
		"  linear issue new --team <KEY> --title <title> --as <actor> [--description <text>] [--label <name> ...]",
		"  linear issue state <ISSUE> --state <name> --as <actor>",
		"  linear issue update <ISSUE> --as <actor> [--description-file <path>] [--priority <priority>] [--project <project>] [--milestone <milestone>] [--title <title>]",
		"  linear issue label add|remove <ISSUE> --label <name> [--label <name> ...] --as <actor>",
		"  linear issue relation add|remove <ISSUE> (--blocks <OTHER> | --blocked-by <OTHER>) --as <actor>",
	} {
		if !strings.Contains(stdout.String(), command+"\n") {
			t.Errorf("issue help does not contain aligned command %q:\n%s", command, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "\n\t  linear issue update") ||
		strings.Contains(stdout.String(), "\n\t  linear issue label") ||
		strings.Contains(stdout.String(), "\n\t  linear issue relation") {
		t.Fatalf("issue help contains tab-indented maintenance command:\n%s", stdout.String())
	}
}

func TestRootHelpAlignsIssueMaintenanceExamples(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := execute([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)
	if _, ok := err.(helpShown); !ok {
		t.Fatalf("execute returned %v, want helpShown", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	for _, command := range []string{
		"  linear issue update <ISSUE> --as <actor> [--description-file <path>] [--priority <priority>] [--project <project>] [--milestone <milestone>] [--title <title>]",
		"  linear issue label add|remove <ISSUE> --label <name> [--label <name> ...] --as <actor>",
		"  linear issue relation add|remove <ISSUE> (--blocks <OTHER> | --blocked-by <OTHER>) --as <actor>",
		"  linear issue update TRAWL-99 --as coordinator --priority high --project OpenTrawl",
		"  linear issue label add TRAWL-99 --label agent-filed --as coordinator",
		"  linear issue relation add TRAWL-99 --blocked-by TRAWL-98 --as coordinator",
	} {
		if !strings.Contains(stdout.String(), command+"\n") {
			t.Errorf("root help does not contain aligned example %q:\n%s", command, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "\n\t  linear issue update") ||
		strings.Contains(stdout.String(), "\n\t  linear issue label") ||
		strings.Contains(stdout.String(), "\n\t  linear issue relation") {
		t.Fatalf("root help contains tab-indented maintenance command:\n%s", stdout.String())
	}
}
