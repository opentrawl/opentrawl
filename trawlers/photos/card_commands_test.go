package photoscrawl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestCardModelConfigurationRequiresExplicitRouteAndCredential(t *testing.T) {
	valid := CardModelConfig{
		ProviderIdentity: "Synthetic Models", BaseURL: "https://models.example.com/v1",
		Model: "synthetic-vision", CredentialEnv: "SYNTHETIC_MODEL_KEY",
	}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*CardModelConfig){
		"provider":       func(config *CardModelConfig) { config.ProviderIdentity = "" },
		"base URL":       func(config *CardModelConfig) { config.BaseURL = "" },
		"model":          func(config *CardModelConfig) { config.Model = "" },
		"credential env": func(config *CardModelConfig) { config.CredentialEnv = "" },
	} {
		t.Run(name, func(t *testing.T) {
			config := valid
			change(&config)
			if err := config.validate(); err == nil {
				t.Fatal("partial card model configuration passed")
			}
		})
	}
	invalid := valid
	invalid.BaseURL = "models.example.com/v1"
	if err := invalid.validate(); err == nil {
		t.Fatal("model route without a scheme passed")
	}
}

func TestCreateCardRequiresCredentialBeforeAction(t *testing.T) {
	config := CardModelConfig{CredentialEnv: "SYNTHETIC_MISSING_MODEL_KEY"}
	t.Setenv(config.CredentialEnv, "")
	if err := config.requireCredential(); err == nil || !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("missing credential error = %v", err)
	}
	t.Setenv(config.CredentialEnv, "synthetic-secret")
	if err := config.requireCredential(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareCardHumanAndJSONShowTheDecisiveRequest(t *testing.T) {
	result := cardRequestResult{
		Type: "photos.card_request.v1", Photo: "photos:asset/synthetic-photo",
		Provider: "Synthetic Models", Endpoint: "https://models.example.com/v1/chat/completions",
		Model: "synthetic-vision", CredentialEnv: "SYNTHETIC_MODEL_KEY",
		Sends:         []string{"one current photo", "checked Photos evidence"},
		RequestSHA256: strings.Repeat("1", 64), ApprovalSHA256: strings.Repeat("2", 64),
		CallCap: 1, State: "ready",
	}
	var human bytes.Buffer
	if err := writeCardRequest(&trawlkit.Request{Out: &human, Format: output.Text}, result); err != nil {
		t.Fatal(err)
	}
	wantHuman := "Photos card ready to approve\n\n" +
		"Photo           photos:asset/synthetic-photo\n" +
		"Sends           one current photo and its checked Photos evidence\n" +
		"Provider        Synthetic Models\n" +
		"Endpoint        https://models.example.com/v1/chat/completions\n" +
		"Model           synthetic-vision\n" +
		"Credential env  SYNTHETIC_MODEL_KEY\n" +
		"Call limit      1\n" +
		"Status          Nothing has been sent\n\n" +
		"Approval        " + strings.Repeat("2", 64) + "\n\n" +
		"Create this card:\n" +
		"  trawl photos create-card " + strings.Repeat("2", 64) + "\n"
	if human.String() != wantHuman {
		t.Fatalf("prepare-card human output =\n%s\nwant:\n%s", human.String(), wantHuman)
	}
	t.Logf("prepare-card human output:\n%s", human.String())
	var machine bytes.Buffer
	if err := writeCardRequest(&trawlkit.Request{Out: &machine, Format: output.JSON}, result); err != nil {
		t.Fatal(err)
	}
	var decoded cardRequestResult
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil {
		t.Fatalf("prepare-card JSON: %v\n%s", err, machine.String())
	}
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("prepare-card JSON = %#v, want %#v", decoded, result)
	}
	t.Logf("prepare-card JSON output:\n%s", machine.String())
}

func TestCreateCardReportsCreatedAndCompletedReplayTruthfully(t *testing.T) {
	for _, test := range []struct {
		state string
		want  string
	}{
		{state: "created", want: "Card created\n\nPhoto  photos:asset/synthetic-photo\n\nOpen it with:\n  trawl photos open photos:asset/synthetic-photo\n"},
		{state: "already_created", want: "Card already exists\n\nPhoto  photos:asset/synthetic-photo\n\nNo model request was sent.\n\nOpen it with:\n  trawl photos open photos:asset/synthetic-photo\n"},
	} {
		t.Run(test.state, func(t *testing.T) {
			var outputBuffer bytes.Buffer
			result := cardCreationResult{Type: "photos.card_creation.v1", Photo: "photos:asset/synthetic-photo", Model: "synthetic-vision", State: test.state}
			if err := writeCardCreation(&trawlkit.Request{Out: &outputBuffer, Format: output.Text}, result); err != nil {
				t.Fatal(err)
			}
			if outputBuffer.String() != test.want {
				t.Fatalf("create-card output =\n%s\nwant:\n%s", outputBuffer.String(), test.want)
			}
			t.Logf("create-card %s output:\n%s", test.state, outputBuffer.String())
			var machine bytes.Buffer
			if err := writeCardCreation(&trawlkit.Request{Out: &machine, Format: output.JSON}, result); err != nil {
				t.Fatal(err)
			}
			var decoded cardCreationResult
			if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil || decoded != result {
				t.Fatalf("create-card JSON decoded=%#v err=%v\n%s", decoded, err, machine.String())
			}
			t.Logf("create-card %s JSON output:\n%s", test.state, machine.String())
		})
	}
}

func TestPreparedCardStorePublishesWholeImmutableFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prepared-cards", strings.Repeat("a", 64)+".pb")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	first := []byte("synthetic complete bundle")
	if err := writePreparedCardOnce(path, first); err != nil {
		t.Fatal(err)
	}
	if err := writePreparedCardOnce(path, first); err != nil {
		t.Fatal(err)
	}
	if err := writePreparedCardOnce(path, []byte("different bytes")); err == nil {
		t.Fatal("immutable prepared request was replaced")
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, first) || info.Mode().Perm() != 0o600 || len(entries) != 1 {
		t.Fatalf("stored=%q mode=%#o entries=%v", stored, info.Mode().Perm(), entries)
	}
}

func TestApprovalDigestMustBeLowercaseHex(t *testing.T) {
	for _, value := range []string{strings.Repeat("a", 64), strings.Repeat("0", 64)} {
		if !validApprovalDigest(value) {
			t.Fatalf("valid digest rejected: %q", value)
		}
	}
	for _, value := range []string{strings.Repeat("A", 64), strings.Repeat("g", 64), "sha256:" + strings.Repeat("a", 64), "../" + strings.Repeat("a", 64)} {
		if validApprovalDigest(value) {
			t.Fatalf("invalid digest passed: %q", value)
		}
	}
}

func TestCardCommandsAreNormalOneArgumentVerbs(t *testing.T) {
	commands := map[string]trawlkit.Verb{}
	for _, command := range New().Verbs() {
		commands[command.Name] = command
	}
	for name, argument := range map[string]string{"prepare-card": "PHOTO", "create-card": "APPROVAL"} {
		command, found := commands[name]
		if !found {
			t.Fatalf("%s is not registered", name)
		}
		if !command.Mutates || !command.Secondary || command.Store != trawlkit.StoreNone || len(command.Args) != 1 || command.Args[0] != argument {
			t.Fatalf("%s command = %#v", name, command)
		}
	}
}

func TestCardCommandsStopBeforeWritesResumeRetainedSuccessAndReplayReadOnly(t *testing.T) {
	ctx := context.Background()
	fixture := prepareCardCommandFixture(t, ctx)
	var requests atomic.Int64
	var removeQueue atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-secret" {
			t.Errorf("model boundary path=%q authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if removeQueue.CompareAndSwap(true, false) {
			db, err := store.Open(ctx, store.Options{Path: fixture.paths.Database, Schema: archive.Schema, SchemaVersion: archive.SchemaVersion})
			if err != nil {
				t.Errorf("open archive in synthetic provider: %v", err)
				http.Error(w, "fixture failed", http.StatusInternalServerError)
				return
			}
			_, err = db.DB().ExecContext(ctx, `delete from classification_queue where id = ?`, fixture.queue.ID)
			_ = db.Close()
			if err != nil {
				t.Errorf("remove queue in synthetic provider: %v", err)
				http.Error(w, "fixture failed", http.StatusInternalServerError)
				return
			}
		}
		arguments, _ := json.Marshal(map[string]any{
			"summary": "Synthetic card", "description": "A two-pixel synthetic image.",
			"venue_plausibility": map[string]string{"candidate_id": "none", "verdict": "none", "reason": "No place claim."},
			"ocr_text":           "", "uncertainties": []string{},
		})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{
			"tool_calls": []map[string]any{{"type": "function", "function": map[string]any{"name": "submit_photo_card", "arguments": string(arguments)}}},
		}}}})
	}))
	t.Cleanup(server.Close)

	credentialEnv := "SYNTHETIC_CARD_COMMAND_KEY"
	crawler := New()
	crawler.cfg.CardModel = CardModelConfig{
		ProviderIdentity: "Synthetic Models", BaseURL: server.URL + "/v1",
		Model: "synthetic-vision", CredentialEnv: credentialEnv,
	}
	prepareOutput := new(bytes.Buffer)
	prepareRequest := fixture.request(output.JSON, prepareOutput, fixture.assetRef)
	if err := crawler.runPrepareCard(ctx, prepareRequest); err != nil {
		t.Fatal(err)
	}
	var prepared cardRequestResult
	if err := json.Unmarshal(prepareOutput.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare output: %v\n%s", err, prepareOutput.String())
	}
	if prepared.State != "ready" || requests.Load() != 0 {
		t.Fatalf("prepared=%#v requests=%d", prepared, requests.Load())
	}

	movedCurrentStills := fixture.paths.OriginalsCacheDir() + "-moved"
	if err := os.Rename(fixture.paths.OriginalsCacheDir(), movedCurrentStills); err != nil {
		t.Fatal(err)
	}
	staleErr := crawler.runCreateCard(ctx, fixture.request(output.JSON, new(bytes.Buffer), prepared.ApprovalSHA256))
	if staleErr == nil || !strings.Contains(staleErr.Error(), "stale") {
		t.Fatalf("missing current-still error = %v", staleErr)
	}
	if err := os.Rename(movedCurrentStills, fixture.paths.OriginalsCacheDir()); err != nil {
		t.Fatal(err)
	}
	assertNoCardCommandWrites(t, ctx, fixture.paths.Database)

	t.Setenv(credentialEnv, "")
	missingCredentialErr := crawler.runCreateCard(ctx, fixture.request(output.JSON, new(bytes.Buffer), prepared.ApprovalSHA256))
	if missingCredentialErr == nil || !strings.Contains(missingCredentialErr.Error(), "is unavailable") {
		t.Fatalf("missing credential error = %v", missingCredentialErr)
	}
	assertNoCardCommandWrites(t, ctx, fixture.paths.Database)

	t.Setenv(credentialEnv, "synthetic-secret")
	removeQueue.Store(true)
	firstErr := crawler.runCreateCard(ctx, fixture.request(output.JSON, new(bytes.Buffer), prepared.ApprovalSHA256))
	if firstErr == nil || !strings.Contains(firstErr.Error(), "read approved card queue") || requests.Load() != 1 {
		t.Fatalf("retained-result setup error=%v requests=%d", firstErr, requests.Load())
	}
	fixture.restoreQueue(t, ctx)
	createdOutput := new(bytes.Buffer)
	if err := crawler.runCreateCard(ctx, fixture.request(output.JSON, createdOutput, prepared.ApprovalSHA256)); err != nil {
		t.Fatal(err)
	}
	var created cardCreationResult
	if err := json.Unmarshal(createdOutput.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.State != "created" || requests.Load() != 1 {
		t.Fatalf("retained result resume=%#v requests=%d", created, requests.Load())
	}

	t.Setenv(credentialEnv, "")
	replayOutput := new(bytes.Buffer)
	if err := crawler.runCreateCard(ctx, fixture.request(output.JSON, replayOutput, prepared.ApprovalSHA256)); err != nil {
		t.Fatal(err)
	}
	var replay cardCreationResult
	if err := json.Unmarshal(replayOutput.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.State != "already_created" || requests.Load() != 1 {
		t.Fatalf("completed replay=%#v requests=%d", replay, requests.Load())
	}

	read, err := store.OpenReadOnly(ctx, fixture.paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	openedOutput := new(bytes.Buffer)
	if err := crawler.Open(ctx, &trawlkit.Request{Store: read, Out: openedOutput, Format: output.Text}, fixture.assetRef); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(openedOutput.String(), "Synthetic card") || strings.Contains(openedOutput.String(), credentialEnv) {
		t.Fatalf("normal open output:\n%s", openedOutput.String())
	}
}

type cardCommandFixture struct {
	paths    archive.Paths
	assetRef string
	queue    cardCommandQueue
}

type cardCommandQueue struct {
	ID, AssetID, SourceLibraryID, State, Reason, UpdatedAt string
	NeedsDownload                                          int
}

func prepareCardCommandFixture(t *testing.T, ctx context.Context) cardCommandFixture {
	t.Helper()
	root := t.TempDir()
	paths := archive.Paths{DataDir: root, Database: filepath.Join(root, "photos.db"), CacheDir: filepath.Join(root, "cache")}
	libraryPath := filepath.Join(root, "Synthetic Photos Library.photoslibrary")
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(root, "synthetic.jpeg")
	imageBytes := syntheticCardCommandImage(t)
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	modification := "2026-07-14T09:00:00.123Z"
	snapshot := photos.LibrarySnapshot{
		Provider: "synthetic", PhotosVersion: "fixture", AuthorizationStatus: "authorized",
		Completeness: photos.SnapshotCompleteness{State: photos.SnapshotComplete, Evidence: map[string]string{"fixture": "complete"}},
		Assets: []photos.Asset{{
			LocalIdentifier: "synthetic-card", MediaType: "image", CreationDate: "2026-07-14T09:00:00Z",
			ModificationDate: modification, Width: 2, Height: 2,
			Resources: []photos.Resource{{Type: "local_original", UTI: "public.jpeg", OriginalFilename: "synthetic.jpeg", LocalPath: imagePath, Availability: "local", AvailableLocally: true}},
		}},
	}
	provider := cardCommandProvider{snapshot: snapshot}
	now := func() time.Time { return time.Date(2026, 7, 14, 9, 5, 0, 0, time.UTC) }
	synced, err := archive.Sync(ctx, paths, archive.SyncOptions{LibraryPath: libraryPath, Provider: provider, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Classify(ctx, paths, archive.ClassifyOptions{Now: now}); err != nil {
		t.Fatal(err)
	}
	read, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	var assetID string
	var queue cardCommandQueue
	err = read.DB().QueryRowContext(ctx, `select a.id, q.id, q.asset_id, q.source_library_id, q.state, q.reason, q.needs_download, q.updated_at from asset a join classification_queue q on q.asset_id=a.id where a.local_identifier='synthetic-card'`).Scan(
		&assetID, &queue.ID, &queue.AssetID, &queue.SourceLibraryID, &queue.State, &queue.Reason, &queue.NeedsDownload, &queue.UpdatedAt,
	)
	_ = read.Close()
	if err != nil {
		t.Fatal(err)
	}
	parsedModification, err := photos.ParseCurrentStillModification(modification)
	if err != nil {
		t.Fatal(err)
	}
	freshness, err := photos.CurrentStillFreshnessForModification(parsedModification)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := photos.NewCurrentStillResolver(paths.OriginalsCacheDir(), func(_ context.Context, _ photos.CurrentStillRequest, destination string) (photos.CurrentStillFact, error) {
		if err := os.WriteFile(destination, imageBytes, 0o600); err != nil {
			return photos.CurrentStillFact{}, err
		}
		digest := sha256.Sum256(imageBytes)
		return photos.CurrentStillFact{MediaType: "public.jpeg", Orientation: 1, PixelWidth: 2, PixelHeight: 2, Size: int64(len(imageBytes)), SHA256: fmt.Sprintf("%x", digest)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(ctx, photos.CurrentStillRequest{SourceLibraryID: synced.SourceLibraryID, AssetUUID: "synthetic-card", PhotoKitLocalIdentifier: "synthetic-card", Freshness: freshness})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Lease != nil {
		resolved.Lease.Close()
	}
	prepared, err := archive.PrepareCardInputAudit(ctx, archive.CardInputAuditPrepareOptions{
		CardInputAuditInventoryOptions: archive.CardInputAuditInventoryOptions{ArchivePath: paths.Database, SourceLibraryID: synced.SourceLibraryID},
		CacheDir:                       paths.CacheDir, AssetID: assetID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.StopReason != "" {
		t.Fatalf("checked card input stopped: %s", prepared.StopReason)
	}
	return cardCommandFixture{paths: paths, assetRef: archive.AssetRef(assetID), queue: queue}
}

type cardCommandProvider struct{ snapshot photos.LibrarySnapshot }

func (p cardCommandProvider) Snapshot(context.Context, string) (photos.LibrarySnapshot, error) {
	return p.snapshot, nil
}

func (f cardCommandFixture) request(format output.Format, out *bytes.Buffer, argument string) *trawlkit.Request {
	return &trawlkit.Request{Paths: trawlkit.Paths{Archive: f.paths.Database}, Format: format, Out: out, Args: []string{argument}}
}

func (f cardCommandFixture) restoreQueue(t *testing.T, ctx context.Context) {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: f.paths.Database, Schema: archive.Schema, SchemaVersion: archive.SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.DB().ExecContext(ctx, `insert into classification_queue(id,asset_id,source_library_id,state,reason,needs_download,updated_at) values(?,?,?,?,?,?,?)`,
		f.queue.ID, f.queue.AssetID, f.queue.SourceLibraryID, f.queue.State, f.queue.Reason, f.queue.NeedsDownload, f.queue.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
}

func assertNoCardCommandWrites(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	db, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, table := range []string{"paid_call_stage", "paid_call_claim", "model_generation_attempt"} {
		var count int
		if err := db.DB().QueryRowContext(ctx, "select count(*) from "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows=%d before approval send", table, count)
		}
	}
}

func syntheticCardCommandImage(t *testing.T) []byte {
	t.Helper()
	fixture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	fixture.Set(0, 0, color.NRGBA{R: 24, G: 48, B: 96, A: 255})
	fixture.Set(1, 1, color.NRGBA{R: 240, G: 192, B: 48, A: 255})
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, fixture, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
