package photoscrawl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const trawlRenderedBinaryEnv = "TRAWL_239_TRAWL_BINARY"

func TestSourceStateRendersThroughTrawlBinary(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv(trawlRenderedBinaryEnv))
	if binary == "" {
		t.Skip("set TRAWL_239_TRAWL_BINARY to run the synthetic trawl rendering proof")
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		t.Fatalf("trawl binary %q is unavailable: %v", binary, err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	libraryPath := filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	createSyntheticLibrary(t, libraryPath)
	writeRenderedTrawlConfig(t, home, libraryPath)

	runRenderedTrawl(t, binary, "current_sync_json", "sync", "photos", "--json").requireSuccess(t)
	runRenderedTrawl(t, binary, "current_sync_human", "sync", "photos").requireSuccess(t)
	ref := assertRenderedSearchAndOpen(t, binary, "current", "current")

	setSyntheticTrashed(t, libraryPath, 1)
	runRenderedTrawl(t, binary, "deleted_sync_json", "sync", "photos", "--json").requireSuccess(t)
	runRenderedTrawl(t, binary, "deleted_sync_human", "sync", "photos").requireSuccess(t)
	if restoredRef := assertRenderedSearchAndOpen(t, binary, "deleted", "deleted_upstream"); restoredRef != ref {
		t.Fatalf("deleted search ref = %q, want retained ref %q", restoredRef, ref)
	}

	setSyntheticTrashed(t, libraryPath, 0)
	runRenderedTrawl(t, binary, "restored_sync_json", "sync", "photos", "--json").requireSuccess(t)
	runRenderedTrawl(t, binary, "restored_sync_human", "sync", "photos").requireSuccess(t)
	if restoredRef := assertRenderedSearchAndOpen(t, binary, "restored", "current"); restoredRef != ref {
		t.Fatalf("restored search ref = %q, want original ref %q", restoredRef, ref)
	}
}

type renderedTrawlRun struct {
	stdout string
	stderr string
	code   int
}

func runRenderedTrawl(t *testing.T, binary, boundary string, args ...string) renderedTrawlRun {
	t.Helper()
	input, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=trawl_%s input=%s", boundary, input)
	cmd := exec.Command(binary, args...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	run := renderedTrawlRun{stdout: stdout.String(), stderr: stderr.String()}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run trawl %v: %v", args, err)
		}
		run.code = exitErr.ExitCode()
	}
	t.Logf("boundary=trawl_%s stdout=%s", boundary, run.stdout)
	t.Logf("boundary=trawl_%s stderr=%s", boundary, run.stderr)
	t.Logf("boundary=trawl_%s exit_code=%d", boundary, run.code)
	return run
}

func (r renderedTrawlRun) requireSuccess(t *testing.T) {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("trawl exit=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
}

func assertRenderedSearchAndOpen(t *testing.T, binary, stateName, wantState string) string {
	t.Helper()
	jsonSearch := runRenderedTrawl(t, binary, stateName+"_search_json", "search", "synthetic", "--source", "photos", "--json")
	jsonSearch.requireSuccess(t)
	var search struct {
		Results []struct {
			Ref     string `json:"ref"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(jsonSearch.stdout), &search); err != nil {
		t.Fatalf("decode %s search JSON: %v\n%s", stateName, err, jsonSearch.stdout)
	}
	if len(search.Results) != 1 || strings.TrimSpace(search.Results[0].Ref) == "" {
		t.Fatalf("%s search results = %#v", stateName, search.Results)
	}
	if wantState == "deleted_upstream" && !strings.HasPrefix(search.Results[0].Snippet, "Deleted upstream · ") {
		t.Fatalf("deleted search snippet = %q", search.Results[0].Snippet)
	}
	if wantState == "current" && strings.HasPrefix(search.Results[0].Snippet, "Deleted upstream · ") {
		t.Fatalf("current search snippet retained deletion prefix: %q", search.Results[0].Snippet)
	}

	humanSearch := runRenderedTrawl(t, binary, stateName+"_search_human", "search", "synthetic", "--source", "photos")
	humanSearch.requireSuccess(t)
	if wantState == "deleted_upstream" && !strings.Contains(humanSearch.stdout, "Deleted upstream") {
		t.Fatalf("deleted human search = %q", humanSearch.stdout)
	}

	ref := search.Results[0].Ref
	jsonOpen := runRenderedTrawl(t, binary, stateName+"_open_json", "open", ref, "--json")
	jsonOpen.requireSuccess(t)
	var opened struct {
		Mechanical struct {
			Source struct {
				State string `json:"state"`
			} `json:"source"`
		} `json:"mechanical"`
	}
	if err := json.Unmarshal([]byte(jsonOpen.stdout), &opened); err != nil {
		t.Fatalf("decode %s open JSON: %v\n%s", stateName, err, jsonOpen.stdout)
	}
	if opened.Mechanical.Source.State != wantState {
		t.Fatalf("%s open source state = %q, want %q", stateName, opened.Mechanical.Source.State, wantState)
	}

	humanOpen := runRenderedTrawl(t, binary, stateName+"_open_human", "open", ref)
	humanOpen.requireSuccess(t)
	if wantState == "deleted_upstream" && !strings.Contains(humanOpen.stdout, "Source: Deleted upstream") {
		t.Fatalf("deleted human open = %q", humanOpen.stdout)
	}
	if wantState == "current" && strings.Contains(humanOpen.stdout, "Source: Deleted upstream") {
		t.Fatalf("current human open retained deletion state: %q", humanOpen.stdout)
	}
	return ref
}

func writeRenderedTrawlConfig(t *testing.T, home, libraryPath string) {
	t.Helper()
	configPath := filepath.Join(home, ".opentrawl", "photos", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("library_path = %q\n", libraryPath)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setSyntheticTrashed(t *testing.T, libraryPath string, value int) {
	t.Helper()
	db := openSyntheticPhotosDB(t, filepath.Join(libraryPath, "database", "Photos.sqlite"))
	defer func() { _ = db.Close() }()
	if _, err := db.DB().Exec(`update ZASSET set ZTRASHEDSTATE = ? where ZUUID = 'fixture-uuid-1'`, value); err != nil {
		t.Fatal(err)
	}
}
