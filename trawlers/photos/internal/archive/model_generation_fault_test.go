package archive

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestPhotosStopsUncertainGenerationBeforeAndAfterSend(t *testing.T) {
	withSyntheticCurrentStill(t)
	tests := []struct {
		name      string
		afterSend bool
	}{
		{name: "before_send"},
		{name: "after_transmission_before_retention", afterSend: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			paths := syntheticGenerationClassifyFixture(t)
			var calls atomic.Int64
			server := syntheticGenerationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if test.afterSend {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Errorf("hijack synthetic connection: %v", err)
						return
					}
					_ = conn.Close()
					return
				}
				writeSyntheticModelResponse(t, w)
			}))
			var faultRaw model.RawResult
			setModelGenerationFault(t, func(stage modelGenerationFaultStage, raw model.RawResult) error {
				switch {
				case !test.afterSend && stage == modelGenerationFaultBeforeSend:
					return errors.New("synthetic interruption before send")
				case test.afterSend && stage == modelGenerationFaultAfterSend:
					faultRaw = raw
					return errors.New("synthetic interruption after transmission")
				default:
					return nil
				}
			})

			logs := &recordingClassifyLogSink{}
			result, err := Classify(ctx, paths, ClassifyOptions{
				Model:    "fixture-model",
				ModelURL: server.URL + "/v1",
				Now:      fixedClock("2026-07-11T10:00:00Z"),
				LogSink:  logs,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.ContentStoppedUncertain != 1 || result.ContentOutcomeTotal != 1 {
				t.Fatalf("stopped result = %#v", result)
			}
			wantCalls := int64(0)
			if test.afterSend {
				wantCalls = 1
				if !faultRaw.TransmissionStarted || len(faultRaw.Failure) == 0 {
					t.Fatalf("HTTP trace boundary did not mark sent failure: %#v", faultRaw)
				}
			}
			if calls.Load() != wantCalls {
				t.Fatalf("provider calls = %d, want %d", calls.Load(), wantCalls)
			}

			attempt, queue := readGenerationFaultState(t, ctx, paths)
			if attempt.Retained || attempt.Response != "" || attempt.Failure != "" {
				t.Fatalf("uncertain attempt retained output: %#v", attempt)
			}
			if attempt.TransmissionStarted != test.afterSend {
				t.Fatalf("attempt transmission state = %#v", attempt)
			}
			if queue.State != "content_failed" || queue.Reason != "stopped_uncertain: model attempt has no retained result" {
				t.Fatalf("stopped queue = %#v", queue)
			}
			assertRecordedLogEvent(t, logs, "stopped_uncertain")

			setModelGenerationFault(t, nil)
			restarted, err := Classify(ctx, paths, ClassifyOptions{
				Model:    "fixture-model",
				ModelURL: server.URL + "/v1",
				Now:      fixedClock("2026-07-11T10:05:00Z"),
			})
			if err != nil {
				t.Fatal(err)
			}
			if restarted.Processed != 0 || calls.Load() != wantCalls {
				t.Fatalf("stopped restart = %#v calls=%d", restarted, calls.Load())
			}
			t.Logf("RAW stopped uncertain attempt: %+v", attempt)
			t.Logf("RAW stopped uncertain queue: %+v", queue)
		})
	}
}

func TestPhotosResumesAfterRawResponseRetentionBeforeParse(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := syntheticGenerationClassifyFixture(t)
	var calls atomic.Int64
	server := syntheticGenerationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeSyntheticModelResponse(t, w)
	}))
	setModelGenerationFault(t, func(stage modelGenerationFaultStage, raw model.RawResult) error {
		if stage == modelGenerationFaultAfterRetain {
			return errors.New("synthetic interruption after response retention")
		}
		return nil
	})
	_, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-model",
		ModelURL: server.URL + "/v1",
		Now:      fixedClock("2026-07-11T10:10:00Z"),
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic interruption after response retention") {
		t.Fatalf("first classify error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
	attempt, queue := readGenerationFaultState(t, ctx, paths)
	if !attempt.Retained || attempt.Response == "" || attempt.Failure != "" || queue.State != "pending" {
		t.Fatalf("retained-before-parse state: attempt=%#v queue=%#v", attempt, queue)
	}

	setModelGenerationFault(t, nil)
	restarted, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-model",
		ModelURL: server.URL + "/v1",
		Now:      fixedClock("2026-07-11T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if restarted.ContentClassified != 1 || restarted.ModelCallAttempts != 0 || calls.Load() != 1 {
		t.Fatalf("retained restart = %#v calls=%d", restarted, calls.Load())
	}
	assertCompleteSyntheticParserAndObservations(t, ctx, paths)
}

func TestPhotosRetainsHTTPTraceTimeoutAndConnectionFailures(t *testing.T) {
	withSyntheticCurrentStill(t)
	tests := []struct {
		name     string
		handler  func(*testing.T, http.ResponseWriter, *http.Request)
		deadline bool
	}{
		{
			name:     "timeout",
			deadline: true,
		},
		{
			name: "connection_closed_after_write",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("hijack synthetic connection: %v", err)
					return
				}
				_ = conn.Close()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := syntheticGenerationClassifyFixture(t)
			var calls atomic.Int64
			releaseTimeoutHandler := make(chan struct{})
			server := syntheticGenerationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if test.deadline {
					<-releaseTimeoutHandler
					return
				}
				test.handler(t, w, r)
			}))
			ctx := context.Background()
			cancel := func() {}
			if test.deadline {
				ctx, cancel = context.WithTimeout(ctx, time.Second)
			}
			_, err := Classify(ctx, paths, ClassifyOptions{
				Model:    "fixture-model",
				ModelURL: server.URL + "/v1",
				Now:      fixedClock("2026-07-11T10:20:00Z"),
			})
			cancel()
			if test.deadline {
				close(releaseTimeoutHandler)
			}
			if test.deadline {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("timeout classify error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("provider calls = %d, want 1", calls.Load())
			}
			attempt, _ := readGenerationFaultState(t, context.Background(), paths)
			if !attempt.Retained || !attempt.TransmissionStarted || attempt.Failure == "" || attempt.Response != "" {
				t.Fatalf("trace-retained failure = %#v", attempt)
			}

			restarted, err := Classify(context.Background(), paths, ClassifyOptions{
				Model:    "fixture-model",
				ModelURL: server.URL + "/v1",
				Now:      fixedClock("2026-07-11T10:25:00Z"),
			})
			if err != nil {
				t.Fatal(err)
			}
			if restarted.ModelCallAttempts != 0 || calls.Load() != 1 {
				t.Fatalf("trace failure restart = %#v calls=%d", restarted, calls.Load())
			}
			t.Logf("RAW HTTP-trace retained %s failure: %+v", test.name, attempt)
		})
	}
}

type generationAttemptState struct {
	GenerationID        string
	TransmissionStarted bool
	Retained            bool
	Response            string
	Failure             string
	Status              int
	ProviderRequestID   string
}

type generationQueueState struct {
	State  string
	Reason string
}

func readGenerationFaultState(t *testing.T, ctx context.Context, paths Paths) (generationAttemptState, generationQueueState) {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var attempt generationAttemptState
	if err := db.DB().QueryRowContext(ctx, `
select g.id, a.transmission_started, a.retained_at is not null,
       coalesce(a.response_body, ''), coalesce(a.failure_body, ''),
       a.http_status, a.provider_request_id
from model_generation g
join model_generation_attempt a on a.generation_id = g.id
`).Scan(
		&attempt.GenerationID,
		&attempt.TransmissionStarted,
		&attempt.Retained,
		&attempt.Response,
		&attempt.Failure,
		&attempt.Status,
		&attempt.ProviderRequestID,
	); err != nil {
		t.Fatal(err)
	}
	var queue generationQueueState
	if err := db.DB().QueryRowContext(ctx, `select state, reason from classification_queue`).Scan(&queue.State, &queue.Reason); err != nil {
		t.Fatal(err)
	}
	return attempt, queue
}

func assertCompleteSyntheticParserAndObservations(t *testing.T, ctx context.Context, paths Paths) {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var route, modelID string
	var requestBody, responseBody []byte
	if err := db.DB().QueryRowContext(ctx, `
select g.request_route, g.model_id, g.request_body, a.response_body
from model_generation g
join model_generation_attempt a on a.generation_id = g.id
`).Scan(&route, &modelID, &requestBody, &responseBody); err != nil {
		t.Fatal(err)
	}
	request, err := model.RestoreProviderRequest(route, modelID, requestBody)
	if err != nil {
		t.Fatal(err)
	}
	response, err := model.Parse(request, model.RawResult{Response: responseBody, Status: "200 OK", StatusCode: http.StatusOK})
	if err != nil {
		t.Fatal(err)
	}
	prepared := preparedCardRequest{CandidateByID: map[string]preparedPlaceCandidate{}}
	card, err := parsePhotoCardToolCall(response.ToolCalls, prepared)
	if err != nil {
		t.Fatal(err)
	}
	parserResult, err := json.MarshalIndent(map[string]any{
		"payload":      photoCardPayload(card),
		"observations": observationsFromCard(card, prepared),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	type storedObservation struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Text         string `json:"text"`
		GenerationID string `json:"generation_id"`
	}
	rows, err := db.DB().QueryContext(ctx, `
select id, observation_type, value_text, coalesce(generation_id, '')
from model_observation
order by id
`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	stored := []storedObservation{}
	for rows.Next() {
		var row storedObservation
		if err := rows.Scan(&row.ID, &row.Type, &row.Text, &row.GenerationID); err != nil {
			t.Fatal(err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 || stored[0].GenerationID == "" {
		t.Fatalf("stored model observations = %#v", stored)
	}
	storedRows, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RAW complete synthetic parser result:\n%s", parserResult)
	t.Logf("RAW complete stored model observations:\n%s", storedRows)
}

func syntheticGenerationClassifyFixture(t *testing.T) Paths {
	t.Helper()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := t.TempDir() + "/Synthetic Photos Library.photoslibrary"
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	imagePath := t.TempDir() + "/synthetic.jpeg"
	writeSyntheticImage(t, imagePath)
	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "synthetic",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{{
			LocalIdentifier: "generation-fault-asset",
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    "2026-07-11T09:00:00Z",
			Width:           2,
			Height:          2,
			Resources: []photos.Resource{{
				Type:             "local_original",
				UTI:              "public.jpeg",
				OriginalFilename: "synthetic.jpeg",
				LocalPath:        imagePath,
				Availability:     "local",
				AvailableLocally: true,
			}},
		}},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-07-11T09:30:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	prepareCheckedCardInputForModelTest(t, ctx, paths, libraryPath, "generation-fault-asset")
	return paths
}

func syntheticGenerationServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("sandbox forbids listeners: %v", err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func writeSyntheticModelResponse(t *testing.T, writer http.ResponseWriter) {
	t.Helper()
	writer.Header().Set("X-Request-ID", "request-synthetic-fault")
	if err := json.NewEncoder(writer).Encode(fixtureToolResponse(fixtureCardResponse(
		"Synthetic card summary.",
		"Synthetic card description with a simple fixture scene.",
		"",
		"Synthetic readable text",
		"synthetic uncertainty",
	))); err != nil {
		t.Fatal(err)
	}
}

func setModelGenerationFault(t *testing.T, hook func(modelGenerationFaultStage, model.RawResult) error) {
	t.Helper()
	old := injectModelGenerationFault
	injectModelGenerationFault = hook
	t.Cleanup(func() { injectModelGenerationFault = old })
}
