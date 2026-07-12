package place

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const syntheticAppleResponse = `{
  "reverse_items": [
    {"name":"Example Square","distance_m":8,"address":{"formatted":"Example Square, Example City, Example Country","country":"Example Country","source":"apple_mapkit_reverse"},"source":"apple_mapkit_reverse"},
    {"distance_m":18,"address":{"formatted":"Unnamed reverse item, Example City","source":"apple_mapkit_reverse"},"source":"apple_mapkit_reverse"}
  ],
  "nearby_items": [
    {"name":"Example Far Museum","category":"museum","distance_m":80,"source":"apple_mapkit_local_search"},
    {"category":"cafe","distance_m":12,"source":"apple_mapkit_local_search"}
  ]
}`

const syntheticReverseResponse = `{
  "features": [
    {"id":"reverse-1","properties":{"name":"Example Square","formatted":"Example Square, Example City","categories":["place.square"],"distance":8},"geometry":{"coordinates":[4.8901,52.3601]}},
    {"id":"reverse-2","properties":{"formatted":"Unnamed mapped block, Example City","category":"building","distance":18},"geometry":{"coordinates":[4.8902,52.3602]}}
  ]
}`

const syntheticNearbyResponse = `{
  "features": [
    {"id":"nearby-1","properties":{"name":"Example Trail","formatted":"Example Trail, Example Park","categories":["natural.trail"],"distance":30},"geometry":{"coordinates":[4.8903,52.3603]}},
    {"id":"nearby-2","properties":{"name":"Example Museum","categories":["tourism.museum"],"distance":12},"geometry":{"coordinates":[4.8901,52.3601]}},
    {"id":"nearby-3","properties":{"formatted":"Unnamed mapped feature","categories":["unfamiliar.provider.category"],"distance":20},"geometry":{"coordinates":[4.8902,52.3602]}}
  ]
}`

func TestEvidenceRetainsRawSeparateRecordsAndReusesOnlyCompleteCache(t *testing.T) {
	server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
		"/reverse": {status: http.StatusOK, body: syntheticReverseResponse},
		"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
	})
	defer server.Close()

	input := syntheticEvidenceInput(52.36, 4.89)
	appleCalls := 0
	runner := evidenceRunner{callApple: func(_ context.Context, got Input, radius float64) appleBoundaryOutput {
		appleCalls++
		request, err := appleRequestJSON(got, radius)
		return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse), Err: err}
	}}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	firstOptions := syntheticEvidenceOptions(server, input, filepath.Join(t.TempDir(), "first"), cacheDir)
	first, err := runEvidence(context.Background(), firstOptions, runner)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != evidenceStateComplete || len(first.Records) != 3 {
		t.Fatalf("first evidence result = %#v", first)
	}
	for _, record := range first.Records {
		if record.PreAuthRequestSHA256 != evidenceDigest([]byte(readRawFile(t, record, "request.raw"))) {
			t.Fatalf("request digest mismatch for %s", record.Operation)
		}
		if record.RawResponseSHA256 != evidenceDigest([]byte(readRawFile(t, record, "response.raw"))) {
			t.Fatalf("response digest mismatch for %s", record.Operation)
		}
		if record.StartedAt == "" || record.CompletedAt == "" || record.DurationMilliseconds < 0 {
			t.Fatalf("missing evidence timing for %s: %#v", record.Operation, record)
		}
	}
	for _, record := range first.Records[1:] {
		headers := readRawFile(t, record, "headers.raw")
		if !strings.Contains(headers, "Content-Type: application/json") || record.RawHeadersSHA256 != evidenceDigest([]byte(headers)) {
			t.Fatalf("raw headers were incomplete for %s: %q", record.Operation, headers)
		}
	}
	if got := []string{first.Records[0].ProviderIdentity, first.Records[1].Operation, first.Records[2].Operation}; !slices.Equal(got, []string{"apple", "osm_reverse", "osm_nearby"}) {
		t.Fatalf("record boundaries = %#v", got)
	}
	if len(first.Records[1].Candidates) != 2 || len(first.Records[2].Candidates) != 3 {
		t.Fatalf("complete candidates were not retained: reverse=%#v nearby=%#v", first.Records[1].Candidates, first.Records[2].Candidates)
	}
	appleCandidates := first.Records[0].Candidates
	if len(appleCandidates) != 4 {
		t.Fatalf("complete Apple reverse and nearby items were not retained: %#v", appleCandidates)
	}
	if got := []string{appleCandidates[0].Name, appleCandidates[1].Name, appleCandidates[2].Name, appleCandidates[3].Name}; !slices.Equal(got, []string{"Example Square", "", "Example Far Museum", ""}) {
		t.Fatalf("Apple provider order or nameless items changed: %#v", appleCandidates)
	}
	if got := []int{appleCandidates[0].ProviderIndex, appleCandidates[1].ProviderIndex, appleCandidates[2].ProviderIndex, appleCandidates[3].ProviderIndex}; !slices.Equal(got, []int{0, 1, 0, 1}) {
		t.Fatalf("Apple source indexes changed: %#v", appleCandidates)
	}
	if got := []string{appleCandidates[0].Source, appleCandidates[1].Source, appleCandidates[2].Source, appleCandidates[3].Source}; !slices.Equal(got, []string{"apple_mapkit_reverse", "apple_mapkit_reverse", "apple_mapkit_local_search", "apple_mapkit_local_search"}) {
		t.Fatalf("Apple reverse and nearby boundaries merged: %#v", appleCandidates)
	}
	if first.Records[2].Candidates[1].ProviderID != "nearby-3" || first.Records[2].Candidates[1].Name != "" {
		t.Fatalf("unnamed unfamiliar candidate was hidden: %#v", first.Records[2].Candidates)
	}
	assertRawFile(t, first.Records[0], "request.raw", `{"latitude":52.36,"longitude":4.89,"radius_meters":150}`)
	assertRawFile(t, first.Records[0], "response.raw", syntheticAppleResponse)
	assertRawFile(t, first.Records[1], "response.raw", syntheticReverseResponse)
	assertRawFile(t, first.Records[2], "response.raw", syntheticNearbyResponse)
	t.Logf("RAW APPLE REQUEST %q", readRawFile(t, first.Records[0], "request.raw"))
	t.Logf("RAW APPLE RESPONSE %q", readRawFile(t, first.Records[0], "response.raw"))
	for _, record := range first.Records {
		candidates, err := json.MarshalIndent(record.Candidates, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("PARSED CANDIDATES %s %s", record.Operation, candidates)
	}
	for _, record := range first.Records[1:] {
		request := readRawFile(t, record, "request.raw")
		if strings.Contains(request, "synthetic-secret") {
			t.Fatalf("pre-auth request leaked credential:\n%s", request)
		}
		t.Logf("RAW PRE-AUTH %s %q", record.Operation, request)
		t.Logf("RAW RESPONSE %s %q", record.Operation, readRawFile(t, record, "response.raw"))
	}
	if appleCalls != 1 || len(*requests) != 2 {
		t.Fatalf("first transport calls: apple=%d OSM=%d", appleCalls, len(*requests))
	}
	t.Logf("RAW AUTHENTICATED REQUEST VALUES %#v", *requests)
	for _, request := range *requests {
		if request.Query().Get("syntheticKey") != "synthetic-secret" {
			t.Fatalf("credential injection request = %q", request.RawQuery)
		}
		t.Logf("RAW AUTHENTICATED REQUEST path=%q query=%q", request.Path, request.RawQuery)
	}

	secondOptions := syntheticEvidenceOptions(server, input, filepath.Join(t.TempDir(), "second"), cacheDir)
	second, err := runEvidence(context.Background(), secondOptions, runner)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != evidenceStateComplete || appleCalls != 1 || len(*requests) != 2 {
		t.Fatalf("cache reuse made transport calls: result=%#v apple=%d OSM=%d", second, appleCalls, len(*requests))
	}
	for _, record := range second.Records {
		if !record.Cached {
			t.Fatalf("record was not revalidated from cache: %#v", record)
		}
		t.Logf("RAW CACHE REPLAY REQUEST %s %q", record.Operation, readRawFile(t, record, "request.raw"))
		t.Logf("RAW CACHE REPLAY RESPONSE %s %q", record.Operation, readRawFile(t, record, "response.raw"))
		metadata, err := os.ReadFile(filepath.Join(cacheDir, record.CacheIdentity, "record.json"))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("RAW CACHE RECORD %s %q", record.Operation, metadata)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, first.Records[1].CacheIdentity, "response.raw"), []byte(`{"features":`), 0o600); err != nil {
		t.Fatal(err)
	}
	thirdOptions := syntheticEvidenceOptions(server, input, filepath.Join(t.TempDir(), "third"), cacheDir)
	third, err := runEvidence(context.Background(), thirdOptions, runner)
	var stopped *EvidenceStoppedError
	if !errors.As(err, &stopped) || len(third.Records) != 2 || third.Records[1].StopReason != evidenceStopCacheIncomplete {
		t.Fatalf("tampered cache did not stop at use: result=%#v error=%v", third, err)
	}
	if len(*requests) != 2 || appleCalls != 1 {
		t.Fatalf("tampered cache triggered provider work: HTTP=%d Apple=%d", len(*requests), appleCalls)
	}
}

func TestStoppedEvidenceRetainsRawOutputAndNeverEntersCache(t *testing.T) {
	server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
		"/reverse": {status: http.StatusServiceUnavailable, body: "synthetic unavailable\n"},
		"/nearby":  {status: http.StatusOK, body: `{"features":`},
	})
	defer server.Close()

	appleCalls := 0
	runner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
		appleCalls++
		request, _ := appleRequestJSON(input, radius)
		return appleBoundaryOutput{Request: request, Response: []byte("Apple reverse geocode returned no placemarks"), Err: ErrProviderNoResult}
	}}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	options := syntheticEvidenceOptions(server, syntheticEvidenceInput(52.36, 4.89), filepath.Join(t.TempDir(), "stopped"), cacheDir)
	result, err := runEvidence(context.Background(), options, runner)
	var stopped *EvidenceStoppedError
	if !errors.As(err, &stopped) {
		t.Fatalf("stopped error = %v", err)
	}
	if result.State != evidenceStateStopped || len(result.StopReasons) != 1 {
		t.Fatalf("stopped result = %#v", result)
	}
	assertRawFile(t, result.Records[0], "response.raw", "Apple reverse geocode returned no placemarks")
	if len(result.Records) != 1 || result.Records[0].StopReason != evidenceStopNoResult {
		t.Fatalf("Apple stop record = %#v", result.Records)
	}
	for _, record := range result.Records {
		t.Logf("RAW STOPPED RESPONSE %s %q", record.Operation, readRawFile(t, record, "response.raw"))
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("stopped records entered cache: %#v", entries)
	}
	options.OutputDir = filepath.Join(t.TempDir(), "stopped-again")
	if _, err := runEvidence(context.Background(), options, runner); !errors.As(err, &stopped) {
		t.Fatalf("second stopped error = %v", err)
	}
	if appleCalls != 2 || len(*requests) != 0 {
		t.Fatalf("stopped evidence was reused: apple=%d OSM=%d", appleCalls, len(*requests))
	}
}

func TestCoordinateVariantsKeepExactRequestsAndCacheIdentitiesSeparate(t *testing.T) {
	server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
		"/reverse": {status: http.StatusOK, body: syntheticReverseResponse},
		"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
	})
	defer server.Close()
	runner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
		request, _ := appleRequestJSON(input, radius)
		return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse)}
	}}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	sourceOptions := syntheticEvidenceOptions(server, syntheticEvidenceInput(30.657, 104.066), filepath.Join(t.TempDir(), "source"), cacheDir)
	sourceOptions.CoordinateVariant = "source-coordinate"
	source, err := runEvidence(context.Background(), sourceOptions, runner)
	if err != nil {
		t.Fatal(err)
	}
	hypothesisOptions := syntheticEvidenceOptions(server, syntheticEvidenceInput(30.659, 104.063), filepath.Join(t.TempDir(), "hypothesis"), cacheDir)
	hypothesisOptions.CoordinateVariant = "conversion-hypothesis"
	hypothesis, err := runEvidence(context.Background(), hypothesisOptions, runner)
	if err != nil {
		t.Fatal(err)
	}
	for i := range source.Records {
		if source.Records[i].CacheIdentity == hypothesis.Records[i].CacheIdentity {
			t.Fatalf("coordinate variants shared cache identity for record %d", i)
		}
	}
	if len(*requests) != 4 {
		t.Fatalf("variant OSM calls = %d", len(*requests))
	}
	sourceRequest := readRawFile(t, source.Records[1], "request.raw")
	hypothesisRequest := readRawFile(t, hypothesis.Records[1], "request.raw")
	if !strings.Contains(sourceRequest, "lat=30.6570000") || !strings.Contains(hypothesisRequest, "lat=30.6590000") {
		t.Fatalf("variant requests:\nsource=%s\nhypothesis=%s", sourceRequest, hypothesisRequest)
	}
	t.Logf("RAW CHINA SOURCE REQUEST %q", sourceRequest)
	t.Logf("RAW CHINA HYPOTHESIS REQUEST %q", hypothesisRequest)
}

func TestGeoapifyParserStopsOnEmptyMalformedAndIncompleteResponses(t *testing.T) {
	input := syntheticEvidenceInput(52.36, 4.89)
	cases := []struct {
		name string
		raw  string
	}{
		{"empty body", ""},
		{"malformed body", `{"features":`},
		{"no result", `{"features":[]}`},
		{"incomplete feature", `{"features":[{"properties":{"name":"Example"},"geometry":{"coordinates":[4.89]}}]}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseGeoapifyEvidence([]byte(test.raw), http.StatusOK, input); err == nil {
				t.Fatalf("raw response passed: %q", test.raw)
			}
		})
	}
}

func TestOversizedResponseStopsAndDoesNotEnterItsCache(t *testing.T) {
	server, _ := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
		"/reverse": {status: http.StatusOK, body: strings.Repeat("x", maxRawEvidenceBytes+1)},
		"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
	})
	defer server.Close()
	runner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
		request, _ := appleRequestJSON(input, radius)
		return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse)}
	}}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	options := syntheticEvidenceOptions(server, syntheticEvidenceInput(52.36, 4.89), filepath.Join(t.TempDir(), "oversized"), cacheDir)
	result, err := runEvidence(context.Background(), options, runner)
	var stopped *EvidenceStoppedError
	if !errors.As(err, &stopped) {
		t.Fatalf("oversized response error = %v", err)
	}
	reverse := result.Records[1]
	if reverse.CompletionState != evidenceStateStopped || reverse.StopReason != evidenceStopTooLarge {
		t.Fatalf("oversized response record = %#v", reverse)
	}
	data, err := os.ReadFile(filepath.Join(reverse.RecordDir, "response.raw"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != maxRawEvidenceBytes+1 {
		t.Fatalf("retained oversized response bytes = %d", len(data))
	}
	if _, err := os.Stat(filepath.Join(cacheDir, reverse.CacheIdentity)); !os.IsNotExist(err) {
		t.Fatalf("oversized response cache error = %v", err)
	}
}

func TestCachedResponseBoundAndIdentityAreRevalidatedBeforeReuse(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(t *testing.T, cacheDir string, record EvidenceRecord)
	}{
		{
			name: "oversized response",
			mutate: func(t *testing.T, cacheDir string, record EvidenceRecord) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(cacheDir, record.CacheIdentity, "response.raw"), []byte(strings.Repeat("x", maxRawEvidenceBytes+1)), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mismatched metadata identity",
			mutate: func(t *testing.T, cacheDir string, record EvidenceRecord) {
				t.Helper()
				path := filepath.Join(cacheDir, record.CacheIdentity, "record.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var metadata EvidenceRecord
				if err := json.Unmarshal(data, &metadata); err != nil {
					t.Fatal(err)
				}
				metadata.ProviderIdentity = "different-provider"
				metadata.Operation = "different-operation"
				metadata.CoordinateVariant = "different-variant"
				metadata.CredentialReference = "DIFFERENT_CREDENTIAL_REF"
				data, err = json.MarshalIndent(metadata, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mismatched raw status",
			mutate: func(t *testing.T, cacheDir string, record EvidenceRecord) {
				t.Helper()
				path := filepath.Join(cacheDir, record.CacheIdentity, "record.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var metadata EvidenceRecord
				if err := json.Unmarshal(data, &metadata); err != nil {
					t.Fatal(err)
				}
				metadata.HTTPStatus = http.StatusCreated
				data, err = json.MarshalIndent(metadata, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink response",
			mutate: func(t *testing.T, cacheDir string, record EvidenceRecord) {
				t.Helper()
				dir := filepath.Join(cacheDir, record.CacheIdentity)
				response := filepath.Join(dir, "response.raw")
				if err := os.Rename(response, filepath.Join(dir, "response.saved")); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(filepath.Dir(cacheDir), "outside.raw")
				if err := os.WriteFile(outside, []byte("synthetic outside evidence"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, response); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
				"/reverse": {status: http.StatusOK, body: syntheticReverseResponse},
				"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
			})
			defer server.Close()
			runner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
				request, _ := appleRequestJSON(input, radius)
				return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse)}
			}}
			cacheDir := filepath.Join(t.TempDir(), "cache")
			options := syntheticEvidenceOptions(server, syntheticEvidenceInput(52.36, 4.89), filepath.Join(t.TempDir(), "first"), cacheDir)
			first, err := runEvidence(context.Background(), options, runner)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, cacheDir, first.Records[1])
			options.OutputDir = filepath.Join(t.TempDir(), "second")
			second, err := runEvidence(context.Background(), options, runner)
			var stopped *EvidenceStoppedError
			if !errors.As(err, &stopped) || len(second.Records) != 2 || second.Records[1].StopReason != evidenceStopCacheIncomplete {
				t.Fatalf("invalid cache did not stop: result=%#v error=%v", second, err)
			}
			if len(*requests) != 2 {
				t.Fatalf("invalid cache triggered provider work: requests=%d", len(*requests))
			}
		})
	}
}

func TestCacheMetadataCannotReadOrDiscardOutsideItsRecordDirectory(t *testing.T) {
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
	outside := filepath.Join(root, "outside.raw")
	outsideBytes := []byte("synthetic-secret-outside-cache")
	if err := os.WriteFile(outside, outsideBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(cacheDir, first.Records[1].CacheIdentity, "record.json")
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var record EvidenceRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record.RawResponseFile = "../../outside.raw"
	data, err = json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.OutputDir = filepath.Join(root, "second")
	result, err := runEvidence(context.Background(), opts, runner)
	var stopped *EvidenceStoppedError
	if !errors.As(err, &stopped) || result.Records[1].StopReason != evidenceStopCacheIncomplete || len(*requests) != 2 {
		t.Fatalf("traversal cache result = %#v requests=%d error=%v", result, len(*requests), err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || !bytes.Equal(got, outsideBytes) {
		t.Fatalf("outside cache file changed: %q error=%v", got, err)
	}
}

func TestConfiguredGeoapifyRejectsEveryEndpointQueryString(t *testing.T) {
	options := syntheticEvidenceOptions(&httptest.Server{URL: "https://geo.example.com"}, syntheticEvidenceInput(52.36, 4.89), t.TempDir(), t.TempDir())
	options.Geoapify.HTTPClient = &http.Client{}
	options.Geoapify.ReverseEndpoint = "https://geo.example.com/reverse?language=en"
	if err := validateConfiguredGeoapify(options.Geoapify); err == nil || !strings.Contains(err.Error(), "query string") {
		t.Fatalf("reverse endpoint query error = %v", err)
	}
	options.Geoapify.ReverseEndpoint = "https://geo.example.com/reverse"
	options.Geoapify.NearbyEndpoint = "https://geo.example.com/nearby?"
	if err := validateConfiguredGeoapify(options.Geoapify); err == nil || !strings.Contains(err.Error(), "query string") {
		t.Fatalf("nearby endpoint query error = %v", err)
	}
}

type syntheticHTTPResponse struct {
	status int
	body   string
}

type capturedHTTPRequest struct {
	Method     string
	Host       string
	RequestURI string
	Header     http.Header
	Path       string
	RawQuery   string
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (r capturedHTTPRequest) Query() url.Values {
	values, _ := url.ParseQuery(r.RawQuery)
	return values
}

func syntheticEvidenceServer(t *testing.T, responses map[string]syntheticHTTPResponse) (*httptest.Server, *[]capturedHTTPRequest) {
	t.Helper()
	var mu sync.Mutex
	requests := []capturedHTTPRequest{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests = append(requests, capturedHTTPRequest{
			Method:     request.Method,
			Host:       request.Host,
			RequestURI: request.RequestURI,
			Header:     request.Header.Clone(),
			Path:       request.URL.Path,
			RawQuery:   request.URL.RawQuery,
		})
		mu.Unlock()
		response, ok := responses[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(response.status)
		_, _ = fmt.Fprint(writer, response.body)
	}))
	return server, &requests
}

func syntheticEvidenceInput(latitude, longitude float64) Input {
	return Input{
		AssetID:        "synthetic-asset",
		TakenAt:        "2026-07-11T00:00:00Z",
		Location:       Coordinate{Latitude: latitude, Longitude: longitude},
		AccuracyMeters: 8,
	}
}

func syntheticEvidenceOptions(server *httptest.Server, input Input, outputDir, cacheDir string) EvidenceOptions {
	return EvidenceOptions{
		Input:             input,
		CoordinateVariant: "source-coordinate",
		RadiusMeters:      150,
		OutputDir:         outputDir,
		CacheDir:          cacheDir,
		Geoapify: ConfiguredGeoapifyEvidence{
			ProviderIdentity:    "synthetic-osm",
			ReverseEndpoint:     server.URL + "/reverse",
			NearbyEndpoint:      server.URL + "/nearby",
			CredentialReference: "SYNTHETIC_OSM_KEY",
			CredentialParameter: "syntheticKey",
			Credential:          "synthetic-secret",
			NearbyCategories:    []string{"natural", "tourism.museum"},
			ReverseLimit:        3,
			NearbyLimit:         4,
			HTTPClient:          server.Client(),
		},
	}
}

func assertRawFile(t *testing.T, record EvidenceRecord, name, want string) {
	t.Helper()
	if got := readRawFile(t, record, name); got != want {
		t.Fatalf("%s %s = %q, want %q", record.Operation, name, got, want)
	}
}

func readRawFile(t *testing.T, record EvidenceRecord, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(record.RecordDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAppleBoundaryMismatchStops(t *testing.T) {
	server, _ := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
		"/reverse": {status: http.StatusOK, body: syntheticReverseResponse},
		"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
	})
	defer server.Close()
	runner := evidenceRunner{callApple: func(context.Context, Input, float64) appleBoundaryOutput {
		return appleBoundaryOutput{Request: []byte(`{"latitude":1}`), Response: []byte(syntheticAppleResponse)}
	}}
	options := syntheticEvidenceOptions(server, syntheticEvidenceInput(52.36, 4.89), filepath.Join(t.TempDir(), "mismatch"), filepath.Join(t.TempDir(), "cache"))
	result, err := runEvidence(context.Background(), options, runner)
	var stopped *EvidenceStoppedError
	if !errors.As(err, &stopped) {
		t.Fatalf("Apple mismatch error = %v", err)
	}
	if result.Records[0].CompletionState != evidenceStateStopped || result.Records[0].StopReason != evidenceStopFailed || !strings.Contains(result.Records[0].StopDetail, "does not match") {
		t.Fatalf("Apple mismatch record = %#v", result.Records[0])
	}
}
