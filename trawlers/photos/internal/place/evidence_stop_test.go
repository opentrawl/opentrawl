package place

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestIncompleteGeoapifyEvidenceStopsBeforeCacheOrLaterOperation(t *testing.T) {
	tests := []struct {
		name         string
		reverse      syntheticHTTPResponse
		nearby       syntheticHTTPResponse
		reverseLimit int
		nearbyLimit  int
		wantReason   string
		wantPaths    []string
	}{
		{
			name:         "reverse result reaches requested limit",
			reverse:      syntheticHTTPResponse{status: http.StatusOK, body: syntheticReverseResponse},
			nearby:       syntheticHTTPResponse{status: http.StatusOK, body: syntheticNearbyResponse},
			reverseLimit: 2,
			nearbyLimit:  4,
			wantReason:   evidenceStopSaturated,
			wantPaths:    []string{"/reverse"},
		},
		{
			name:         "nearby result reaches requested limit",
			reverse:      syntheticHTTPResponse{status: http.StatusOK, body: syntheticReverseResponse},
			nearby:       syntheticHTTPResponse{status: http.StatusOK, body: syntheticNearbyResponse},
			reverseLimit: 3,
			nearbyLimit:  3,
			wantReason:   evidenceStopSaturated,
			wantPaths:    []string{"/reverse", "/nearby"},
		},
		{
			name:         "empty reverse response",
			reverse:      syntheticHTTPResponse{status: http.StatusOK, body: ""},
			nearby:       syntheticHTTPResponse{status: http.StatusOK, body: syntheticNearbyResponse},
			reverseLimit: 3,
			nearbyLimit:  4,
			wantReason:   evidenceStopEmpty,
			wantPaths:    []string{"/reverse"},
		},
		{
			name:         "empty-body reverse provider failure",
			reverse:      syntheticHTTPResponse{status: http.StatusServiceUnavailable, body: ""},
			nearby:       syntheticHTTPResponse{status: http.StatusOK, body: syntheticNearbyResponse},
			reverseLimit: 3,
			nearbyLimit:  4,
			wantReason:   evidenceStopFailed,
			wantPaths:    []string{"/reverse"},
		},
		{
			name:         "malformed reverse response",
			reverse:      syntheticHTTPResponse{status: http.StatusOK, body: `{"features":`},
			nearby:       syntheticHTTPResponse{status: http.StatusOK, body: syntheticNearbyResponse},
			reverseLimit: 3,
			nearbyLimit:  4,
			wantReason:   evidenceStopMalformed,
			wantPaths:    []string{"/reverse"},
		},
		{
			name:         "reverse provider failure",
			reverse:      syntheticHTTPResponse{status: http.StatusServiceUnavailable, body: "synthetic unavailable\n"},
			nearby:       syntheticHTTPResponse{status: http.StatusOK, body: syntheticNearbyResponse},
			reverseLimit: 3,
			nearbyLimit:  4,
			wantReason:   evidenceStopFailed,
			wantPaths:    []string{"/reverse"},
		},
		{
			name:         "reverse provider has no result",
			reverse:      syntheticHTTPResponse{status: http.StatusOK, body: `{"features":[]}`},
			nearby:       syntheticHTTPResponse{status: http.StatusOK, body: syntheticNearbyResponse},
			reverseLimit: 3,
			nearbyLimit:  4,
			wantReason:   evidenceStopNoResult,
			wantPaths:    []string{"/reverse"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
				"/reverse": test.reverse,
				"/nearby":  test.nearby,
			})
			defer server.Close()

			appleCalls := 0
			runner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
				appleCalls++
				request, err := appleRequestJSON(input, radius)
				return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse), Err: err}
			}}
			root := t.TempDir()
			cacheDir := filepath.Join(root, "cache")
			options := syntheticEvidenceOptions(server, syntheticEvidenceInput(52.36, 4.89), filepath.Join(root, "first"), cacheDir)
			options.Geoapify.ReverseLimit = test.reverseLimit
			options.Geoapify.NearbyLimit = test.nearbyLimit

			first := runStoppedEvidenceCase(t, options, runner, test.wantReason)
			assertEmptyEvidenceCache(t, cacheDir)
			logRawStoppedEvidence(t, first, *requests)
			if appleCalls != 1 || !slices.Equal(requestPaths(*requests), test.wantPaths) {
				t.Fatalf("first downstream calls: apple=%d OSM=%#v", appleCalls, requestPaths(*requests))
			}

			options.OutputDir = filepath.Join(root, "second")
			second := runStoppedEvidenceCase(t, options, runner, test.wantReason)
			wantRepeatedPaths := append(append([]string(nil), test.wantPaths...), test.wantPaths...)
			t.Logf("RAW RESTART CAPTURED REQUEST VALUES %#v", *requests)
			if appleCalls != 2 || !slices.Equal(requestPaths(*requests), wantRepeatedPaths) {
				t.Fatalf("restart reused stopped evidence: apple=%d OSM=%#v", appleCalls, requestPaths(*requests))
			}
			for _, record := range second.Records {
				if record.Cached {
					t.Fatalf("stopped restart reopened a cache hit: %#v", record)
				}
			}
			assertEmptyEvidenceCache(t, cacheDir)
		})
	}
}

func runStoppedEvidenceCase(t *testing.T, options EvidenceOptions, runner evidenceRunner, wantReason string) EvidenceResult {
	t.Helper()
	result, err := runEvidence(context.Background(), options, runner)
	var stopped *EvidenceStoppedError
	if !errors.As(err, &stopped) {
		t.Fatalf("stopped evidence error = %v", err)
	}
	if result.State != evidenceStateStopped || len(result.StopReasons) != 1 || len(result.Records) < 1 {
		t.Fatalf("stopped evidence result = %#v", result)
	}
	record := result.Records[len(result.Records)-1]
	if record.CompletionState != evidenceStateStopped || record.StopReason != wantReason {
		t.Fatalf("stop record = %#v, want reason %q", record, wantReason)
	}
	return result
}

func assertEmptyEvidenceCache(t *testing.T, cacheDir string) {
	t.Helper()
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RAW CACHE READDIR OUTPUT %#v", entries)
	if len(entries) != 0 {
		t.Fatalf("stopped evidence entered cache: %#v", entries)
	}
}

func requestPaths(requests []capturedHTTPRequest) []string {
	paths := make([]string, 0, len(requests))
	for _, request := range requests {
		paths = append(paths, request.Path)
	}
	return paths
}

func logRawStoppedEvidence(t *testing.T, result EvidenceResult, requests []capturedHTTPRequest) {
	t.Helper()
	for _, record := range result.Records {
		t.Logf("RAW REQUEST %s %q", record.Operation, readRawFile(t, record, "request.raw"))
		t.Logf("RAW RESPONSE %s %q", record.Operation, readRawFile(t, record, "response.raw"))
		parsed, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("PARSED RECORD %s %s", record.Operation, parsed)
	}
	t.Logf("STOP DECISION %#v", result.StopReasons)
	t.Logf("RAW CAPTURED REQUEST VALUES %#v", requests)
	t.Logf("DOWNSTREAM CALL DECISION paths=%#v", requestPaths(requests))
}

func TestGenericAppleFailureIsNotClassifiedAsEmpty(t *testing.T) {
	server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
		"/reverse": {status: http.StatusOK, body: syntheticReverseResponse},
		"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
	})
	defer server.Close()
	runner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
		request, err := appleRequestJSON(input, radius)
		if err != nil {
			return appleBoundaryOutput{Err: err}
		}
		return appleBoundaryOutput{Request: request, Err: errors.New("synthetic Apple operational failure")}
	}}
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	options := syntheticEvidenceOptions(server, syntheticEvidenceInput(52.36, 4.89), filepath.Join(root, "output"), cacheDir)
	result := runStoppedEvidenceCase(t, options, runner, evidenceStopFailed)
	assertEmptyEvidenceCache(t, cacheDir)
	logRawStoppedEvidence(t, result, *requests)
	if len(result.Records) != 1 || len(*requests) != 0 {
		t.Fatalf("generic Apple failure reached a later operation: result=%#v requests=%#v", result, *requests)
	}
}

func TestCredentialNeverEntersRetainedEvidenceOrReturnedError(t *testing.T) {
	credential := "synthetic-secret+/="
	tests := []struct {
		name               string
		marker             string
		wantCredentialStop bool
		response           func(*http.Request) (*http.Response, error)
	}{
		{
			name:               "credential-bearing response",
			wantCredentialStop: true,
			response: func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("provider echoed " + credential)),
					Header:     make(http.Header),
					Request:    request,
				}, nil
			},
		},
		{
			name:   "transport error",
			marker: redactedTransportFailure,
			response: func(request *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("synthetic transport included authenticated URL %s", request.URL.String())
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := syntheticEvidenceInput(52.36, 4.89)
			runner := evidenceRunner{callApple: func(_ context.Context, got Input, radius float64) appleBoundaryOutput {
				request, err := appleRequestJSON(got, radius)
				return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse), Err: err}
			}}
			root := t.TempDir()
			options := EvidenceOptions{
				Input:             input,
				CoordinateVariant: "source-coordinate",
				RadiusMeters:      150,
				OutputDir:         filepath.Join(root, "output"),
				CacheDir:          filepath.Join(root, "cache"),
				Geoapify: ConfiguredGeoapifyEvidence{
					ProviderIdentity:    "synthetic-osm",
					ReverseEndpoint:     "https://geo.example.com/reverse",
					NearbyEndpoint:      "https://geo.example.com/nearby",
					CredentialReference: "SYNTHETIC_OSM_KEY",
					CredentialParameter: "syntheticKey",
					Credential:          credential,
					NearbyCategories:    []string{"natural"},
					ReverseLimit:        3,
					NearbyLimit:         4,
					HTTPClient:          &http.Client{Transport: roundTripFunc(test.response)},
				},
			}
			result, err := runEvidence(context.Background(), options, runner)
			var stopped *EvidenceStoppedError
			if !errors.As(err, &stopped) {
				t.Fatalf("credential safety error = %v", err)
			}
			if len(result.Records) != 2 || strings.Contains(err.Error(), credential) {
				t.Fatalf("unsafe stopped result = %#v error=%v", result, err)
			}
			for _, reason := range result.StopReasons {
				if strings.Contains(reason, credential) {
					t.Fatalf("stop reason leaked credential: %q", reason)
				}
			}
			if test.wantCredentialStop {
				if result.Records[1].StopReason != evidenceStopCredential || result.Records[1].RawResponseFile != "" || result.Records[1].RawHeadersFile != "" {
					t.Fatalf("credential-bearing response retained bytes: %#v", result.Records[1])
				}
			} else if got := readRawFile(t, result.Records[1], "response.raw"); got != test.marker {
				t.Fatalf("retained safety marker = %q", got)
			}
			if err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
				if walkErr != nil || info.IsDir() {
					return walkErr
				}
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				if bytes.Contains(data, []byte(credential)) || bytes.Contains(data, []byte(url.QueryEscape(credential))) {
					return fmt.Errorf("credential leaked into retained artefact %s", path)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCachedCredentialBearingResponseIsDiscardedBeforeProviderCall(t *testing.T) {
	server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
		"/reverse": {status: http.StatusOK, body: syntheticReverseResponse},
		"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
	})
	defer server.Close()
	runner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
		request, _ := appleRequestJSON(input, radius)
		return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse)}
	}}
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	opts := syntheticEvidenceOptions(server, syntheticEvidenceInput(52.36, 4.89), filepath.Join(root, "first"), cacheDir)
	first, err := runEvidence(context.Background(), opts, runner)
	if err != nil {
		t.Fatal(err)
	}
	unsafeResponse := []byte("provider echoed " + opts.Geoapify.Credential)
	cacheRecordDir := filepath.Join(cacheDir, first.Records[1].CacheIdentity)
	if err := os.WriteFile(filepath.Join(cacheRecordDir, "response.raw"), unsafeResponse, 0o600); err != nil {
		t.Fatal(err)
	}
	opts.OutputDir = filepath.Join(root, "second")
	result, err := runEvidence(context.Background(), opts, runner)
	var stopped *EvidenceStoppedError
	if !errors.As(err, &stopped) || result.Records[1].StopReason != evidenceStopCredential {
		t.Fatalf("unsafe cache result = %#v error=%v", result, err)
	}
	if len(*requests) != 2 || strings.Contains(err.Error(), opts.Geoapify.Credential) {
		t.Fatalf("unsafe cache triggered transport or leaked credential: requests=%d error=%v", len(*requests), err)
	}
	for _, name := range []string{"response.raw", "headers.raw"} {
		data, readErr := os.ReadFile(filepath.Join(cacheRecordDir, name))
		if readErr != nil || len(data) != 0 {
			t.Fatalf("unsafe cache %s was not discarded: bytes=%q error=%v", name, data, readErr)
		}
	}
	marker, err := os.ReadFile(filepath.Join(cacheRecordDir, "record.json"))
	if err != nil || !strings.Contains(string(marker), evidenceStopCredential) || bytes.Contains(marker, []byte(opts.Geoapify.Credential)) {
		t.Fatalf("unsafe cache marker = %q error=%v", marker, err)
	}
}
