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

func TestStaleProjectionRendersThroughTrawlBinary(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv(trawlRenderedBinaryEnv))
	if binary == "" {
		t.Skip("set TRAWL_239_TRAWL_BINARY to run the synthetic trawl rendering proof")
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		t.Fatalf("trawl binary %q is unavailable: %v", binary, err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COLUMNS", "140")
	t.Setenv("TZ", "UTC")
	libraryPath := filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	createSyntheticLibrary(t, libraryPath)
	addSyntheticCurrentAsset(t, libraryPath)
	writeRenderedTrawlConfig(t, home, libraryPath)
	runRenderedTrawl(t, binary, "source_sync_initial", "photos", "sync", "--json").requireSuccess(t)
	archivePath := filepath.Join(home, ".opentrawl", "photos", "photos.db")
	seedStaleObservationRows(t, archivePath, "rendered-stale")
	setSyntheticFavorite(t, libraryPath, 0)
	runRenderedTrawl(t, binary, "source_sync_stale", "photos", "sync", "--json").requireSuccess(t)

	sourceJSONSearch := runRenderedTrawl(t, binary, "source_search_json", "photos", "search", "synthetic", "--json")
	sourceJSONSearch.requireSuccess(t)
	var search struct {
		Results []struct {
			Ref      string `json:"ref"`
			ShortRef string `json:"short_ref"`
			Snippet  string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(sourceJSONSearch.stdout), &search); err != nil {
		t.Fatalf("decode source search JSON: %v\n%s", err, sourceJSONSearch.stdout)
	}
	if len(search.Results) != 2 || search.Results[0].ShortRef == "" || search.Results[1].ShortRef == "" {
		t.Fatalf("source search JSON = %s", sourceJSONSearch.stdout)
	}
	var stale, current struct {
		Ref      string `json:"ref"`
		ShortRef string `json:"short_ref"`
		Snippet  string `json:"snippet"`
	}
	for _, result := range search.Results {
		if strings.HasPrefix(result.Snippet, "Stale · ") {
			stale = result
		} else {
			current = result
		}
	}
	if stale.Snippet != "Stale · Synthetic beach scene." || current.ShortRef == "" {
		t.Fatalf("source search snippets = %s", sourceJSONSearch.stdout)
	}
	wantSourceJSON := fmt.Sprintf(`{
  "query": "synthetic",
  "results": [
    {
      "ref": %q,
      "short_ref": %q,
      "time": "2026-05-28T12:00:00+02:00",
      "where": "Synthetic Pier",
      "snippet": "Stale · Synthetic beach scene."
    },
    {
      "ref": %q,
      "short_ref": %q,
      "time": "2026-07-10T11:00:00Z",
      "where": "GPS 52.3676, 4.9041 +/-8m",
      "snippet": "current-synthetic.heic Synthetic Album current-synthetic.heic"
    }
  ],
  "total_matches": 2,
  "truncated": false
}
`, stale.Ref, stale.ShortRef, current.Ref, current.ShortRef)
	if sourceJSONSearch.stdout != wantSourceJSON {
		t.Fatalf("source search JSON =\n%s\nwant:\n%s", sourceJSONSearch.stdout, wantSourceJSON)
	}
	sourceHumanSearch := runRenderedTrawl(t, binary, "source_search_human", "photos", "search", "synthetic")
	sourceHumanSearch.requireSuccess(t)
	wantSourceHuman := fmt.Sprintf("Search \"synthetic\": showing 2 of 2, newest first.\nOpen: trawl photos open REF\n\ndate              where                 ref    text\n2026-05-28 10:00  Synthetic Pier        %s  Stale · Synthetic beach scene.\n2026-07-10 11:00  GPS 52.3676, 4.9041…  %s  current-synthetic.heic Synthetic Album current-synthetic.heic\n", stale.ShortRef, current.ShortRef)
	if sourceHumanSearch.stdout != wantSourceHuman {
		t.Fatalf("source human search =\n%s\nwant:\n%s", sourceHumanSearch.stdout, wantSourceHuman)
	}

	federatedJSONSearch := runRenderedTrawl(t, binary, "federated_search_json", "search", "synthetic", "--source", "photos", "--json")
	federatedJSONSearch.requireSuccess(t)
	if strings.Contains(federatedJSONSearch.stdout, `"short_ref"`) || !strings.Contains(federatedJSONSearch.stdout, current.Ref) || !strings.Contains(federatedJSONSearch.stdout, stale.Ref) {
		t.Fatalf("federated search JSON must keep canonical refs:\n%s", federatedJSONSearch.stdout)
	}

	jsonOpen := runRenderedTrawl(t, binary, "stale_open_json", "photos", "open", stale.ShortRef, "--json")
	jsonOpen.requireSuccess(t)
	for _, want := range []string{
		`"schema_version": 5`,
		`"reason": "source details changed after this card was created"`,
		`"title": "Synthetic Album"`,
		`"latitude": 52.3676`,
		`"longitude": 4.9041`,
	} {
		if !strings.Contains(jsonOpen.stdout, want) {
			t.Fatalf("stale open JSON missing %q:\n%s", want, jsonOpen.stdout)
		}
	}
	if !strings.Contains(jsonOpen.stdout, `"banner": "Card status: Stale · source details changed after this card was created · since `) {
		t.Fatalf("stale open JSON missing banner:\n%s", jsonOpen.stdout)
	}
	for _, forbidden := range []string{"generic_album:2:0", `"address"`} {
		if strings.Contains(jsonOpen.stdout, forbidden) {
			t.Fatalf("stale open JSON leaked %q:\n%s", forbidden, jsonOpen.stdout)
		}
	}

	humanOpen := runRenderedTrawl(t, binary, "stale_open_human", "photos", "open", stale.ShortRef)
	humanOpen.requireSuccess(t)
	for _, want := range []string{
		"Card status: Stale · source details changed after this card was created · since ",
		"GPS: 52.36760, 4.90410, +/-8m",
		"Albums: Synthetic Album",
	} {
		if !strings.Contains(humanOpen.stdout, want) {
			t.Fatalf("stale human open missing %q:\n%s", want, humanOpen.stdout)
		}
	}
	if strings.Contains(humanOpen.stdout, "Address:") {
		t.Fatalf("stale human open invented an address:\n%s", humanOpen.stdout)
	}

	currentJSONOpen := runRenderedTrawl(t, binary, "current_open_json", "photos", "open", current.ShortRef, "--json")
	currentJSONOpen.requireSuccess(t)
	for _, want := range []string{`"title": "Synthetic Album"`, `"latitude": 52.3676`, `"longitude": 4.9041`} {
		if !strings.Contains(currentJSONOpen.stdout, want) {
			t.Fatalf("current open JSON missing %q:\n%s", want, currentJSONOpen.stdout)
		}
	}
	for _, forbidden := range []string{`"stale"`, `"address"`, "generic_album:2:0"} {
		if strings.Contains(currentJSONOpen.stdout, forbidden) {
			t.Fatalf("current open JSON leaked %q:\n%s", forbidden, currentJSONOpen.stdout)
		}
	}
	currentHumanOpen := runRenderedTrawl(t, binary, "current_open_human", "photos", "open", current.ShortRef)
	currentHumanOpen.requireSuccess(t)
	if !strings.Contains(currentHumanOpen.stdout, "GPS: 52.36760, 4.90410, +/-8m") || !strings.Contains(currentHumanOpen.stdout, "Albums: Synthetic Album") || strings.Contains(currentHumanOpen.stdout, "Address:") || strings.Contains(currentHumanOpen.stdout, "Card status: Stale") {
		t.Fatalf("current human open = %s", currentHumanOpen.stdout)
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
	if wantState == "current" && strings.HasPrefix(search.Results[0].Snippet, "Stale · ") {
		t.Fatalf("current search snippet retained stale prefix: %q", search.Results[0].Snippet)
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
	if wantState == "current" && strings.Contains(humanOpen.stdout, "Card status: Stale") {
		t.Fatalf("current human open retained stale status: %q", humanOpen.stdout)
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

func addSyntheticCurrentAsset(t *testing.T, libraryPath string) {
	t.Helper()
	db := openSyntheticPhotosDB(t, filepath.Join(libraryPath, "database", "Photos.sqlite"))
	defer func() { _ = db.Close() }()
	created := coreDataSeconds("2026-07-10T11:00:00Z")
	if _, err := db.DB().Exec(`
insert into ZASSET(Z_PK, ZUUID, ZKIND, ZKINDSUBTYPE, ZDATECREATED, ZMODIFICATIONDATE, ZADDEDDATE, ZWIDTH, ZHEIGHT, ZDURATION, ZFAVORITE, ZHIDDEN, ZAVALANCHEUUID, ZLATITUDE, ZLONGITUDE, ZUNIFORMTYPEIDENTIFIER, ZFILENAME, ZTRASHEDSTATE)
values (2, 'fixture-uuid-2', 0, 0, ?, ?, ?, 4032, 3024, 0, 0, 0, '', 52.3676, 4.9041, 'public.heic', 'current-synthetic.heic', 0)
`, created, created, created); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`insert into ZADDITIONALASSETATTRIBUTES(ZASSET, ZTIMEZONENAME, ZGPSHORIZONTALACCURACY, ZORIGINALFILENAME) values (2, 'UTC', 8, 'current-synthetic.heic')`,
		`insert into ZEXTENDEDATTRIBUTES(ZASSET, ZCAMERAMAKE, ZCAMERAMODEL, ZLENSMODEL, ZFOCALLENGTH, ZFOCALLENGTHIN35MM, ZAPERTURE, ZSHUTTERSPEED, ZISO) values (2, '', '', '', 0, 0, 0, 0, 0)`,
		`insert into ZINTERNALRESOURCE(ZASSET, ZRESOURCETYPE, ZCOMPACTUTI, ZDATALENGTH, ZSTABLEHASH, ZFINGERPRINT, ZLOCALAVAILABILITY, ZREMOTEAVAILABILITY, ZVERSION) values (2, 0, 'public.heic', 1024, 'stable-hash-2', '', 0, 1, 1)`,
		`insert into Z_34ASSETS(Z_34ALBUMS, Z_3ASSETS) values (10, 2)`,
	} {
		if _, err := db.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}
