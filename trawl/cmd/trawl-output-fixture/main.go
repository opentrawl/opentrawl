package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/trawl/internal/cli"
	"github.com/opentrawl/opentrawl/trawl/internal/qa"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == crawlkit.HiddenWireSubcommand {
		os.Exit(cli.ExecuteCrawlerWire(os.Args[1:]))
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		_, _ = fmt.Fprintln(stderr, "usage: trawl-output-fixture HOME REPO_ROOT")
		return 2
	}
	home, repoRoot := args[0], args[1]
	fixtures, err := qa.CreateOutputFixtures(home, repoRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "create fixtures: %v\n", err)
		return 1
	}
	restoreHome := replaceEnv("HOME", home)
	defer restoreHome()
	restoreTZ := replaceEnv("TZ", "UTC")
	defer restoreTZ()
	restorePath := replaceEnv("PATH", fixtures.BinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	defer restorePath()

	setup := [][]string{
		{"--json", "imessage", "sync"},
		{"--json", "telegram", "sync", "--path", fixtures.TelegramSource},
		{"--json", "whatsapp", "sync"},
		{"--json", "photos", "sync"},
		{"--json", "gmail", "sync", "--query", "launch", "--max", "25", "--backup-repo", filepath.Join(home, ".opentrawl", "gmail", "backup")},
		{"--json", "calendar", "sync"},
		{"--json", "twitter", "import", "archive", fixtures.BirdDump},
	}
	for _, command := range setup {
		if err := runCLI(command); err != nil {
			_, _ = fmt.Fprintf(stderr, "setup %s: %v\n", strings.Join(command, " "), err)
			return 1
		}
	}
	if err := qa.AddTelegramLaunchTerm(home); err != nil {
		_, _ = fmt.Fprintf(stderr, "patch telegram fixture: %v\n", err)
		return 1
	}
	refs := map[string]string{}
	for _, source := range []string{"imessage", "telegram", "whatsapp", "photos", "gmail", "calendar", "twitter"} {
		ref, err := firstSearchRef(source)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "find %s open ref: %v\n", source, err)
			return 1
		}
		refs[source] = ref
	}
	_, _ = fmt.Fprintf(stdout, "FIXTURE_HOME=%q\n", home)
	for source, ref := range refs {
		_, _ = fmt.Fprintf(stdout, "OPEN_REF_%s=%q\n", strings.ToUpper(source), ref)
	}
	return 0
}

func runCLI(args []string) error {
	var stdout, stderr bytes.Buffer
	if err := cli.Execute(args, &stdout, &stderr); err != nil {
		return fmt.Errorf("%w\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return nil
}

func firstSearchRef(source string) (string, error) {
	var stdout, stderr bytes.Buffer
	if err := cli.Execute([]string{"--json", source, "search", "launch", "--limit", "1"}, &stdout, &stderr); err != nil {
		return "", fmt.Errorf("%w\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	var envelope struct {
		Results []struct {
			Ref string `json:"ref"`
		} `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return "", err
	}
	if len(envelope.Results) == 0 || strings.TrimSpace(envelope.Results[0].Ref) == "" {
		return "", fmt.Errorf("search returned no ref")
	}
	return envelope.Results[0].Ref, nil
}

func replaceEnv(name, value string) func() {
	old, hadOld := os.LookupEnv(name)
	_ = os.Unsetenv(name)
	_ = os.Setenv(name, value)
	return func() {
		_ = os.Unsetenv(name)
		if hadOld {
			_ = os.Setenv(name, old)
		}
	}
}
