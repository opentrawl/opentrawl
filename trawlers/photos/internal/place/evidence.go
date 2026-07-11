package place

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const (
	evidenceParserVersion = "photos-place-evidence-v1"
	evidenceStateComplete = "complete"
	evidenceStateStopped  = "stopped"
	maxRawEvidenceBytes   = 4 << 20
)

var errRawEvidenceTooLarge = fmt.Errorf("provider response exceeds %d bytes", maxRawEvidenceBytes)

type EvidenceOptions struct {
	Input             Input
	CoordinateVariant string
	RadiusMeters      float64
	OutputDir         string
	CacheDir          string
	Geoapify          ConfiguredGeoapifyEvidence
}

type ConfiguredGeoapifyEvidence struct {
	ProviderIdentity    string
	ReverseEndpoint     string
	NearbyEndpoint      string
	CredentialReference string
	CredentialParameter string
	Credential          string
	NearbyCategories    []string
	ReverseLimit        int
	NearbyLimit         int
	HTTPClient          *http.Client
}

type EvidenceResult struct {
	State             string           `json:"state"`
	CoordinateVariant string           `json:"coordinate_variant"`
	Records           []EvidenceRecord `json:"records"`
	StopReasons       []string         `json:"stop_reasons,omitempty"`
}

type EvidenceStoppedError struct {
	OutputDir   string
	StopReasons []string
}

func (e *EvidenceStoppedError) Error() string {
	reason := strings.Join(e.StopReasons, "; ")
	if reason == "" {
		reason = "one or more provider boundaries were incomplete"
	}
	return fmt.Sprintf("place evidence stopped: %s; inspect private records at %s", reason, e.OutputDir)
}

type EvidenceRecord struct {
	Input               Input               `json:"input"`
	ProviderIdentity    string              `json:"provider_identity"`
	Operation           string              `json:"operation"`
	CoordinateVariant   string              `json:"coordinate_variant"`
	ParserVersion       string              `json:"parser_version"`
	PreAuthRequestFile  string              `json:"pre_auth_request_file"`
	RawResponseFile     string              `json:"raw_response_file"`
	HTTPStatus          int                 `json:"http_status,omitempty"`
	Address             *Address            `json:"address,omitempty"`
	Candidates          []EvidenceCandidate `json:"candidates"`
	CompletionState     string              `json:"completion_state"`
	StopReason          string              `json:"stop_reason,omitempty"`
	CacheIdentity       string              `json:"cache_identity"`
	Cached              bool                `json:"cached,omitempty"`
	RecordDir           string              `json:"record_dir,omitempty"`
	CredentialReference string              `json:"credential_reference,omitempty"`
}

type EvidenceCandidate struct {
	ProviderIndex int         `json:"provider_index"`
	ProviderID    string      `json:"provider_id,omitempty"`
	Name          string      `json:"name,omitempty"`
	Categories    []string    `json:"categories"`
	Coordinate    *Coordinate `json:"coordinate,omitempty"`
	DistanceM     float64     `json:"distance_m,omitempty"`
	Address       *Address    `json:"address,omitempty"`
	Source        string      `json:"source,omitempty"`
}

type evidenceCapture struct {
	record   EvidenceRecord
	request  []byte
	response []byte
}

type parsedEvidence struct {
	address    *Address
	candidates []EvidenceCandidate
}

type evidenceParser func([]byte, int, Input) (parsedEvidence, error)

type evidenceRunner struct {
	callApple func(context.Context, Input, float64) appleBoundaryOutput
}

func LoadEvidenceInput(path string) (Input, error) {
	return loadInput(path)
}

func RunEvidence(ctx context.Context, opts EvidenceOptions) (EvidenceResult, error) {
	return runEvidence(ctx, opts, evidenceRunner{
		callApple: callAppleBoundary,
	})
}

func runEvidence(ctx context.Context, opts EvidenceOptions, runner evidenceRunner) (EvidenceResult, error) {
	if err := validateInput(opts.Input); err != nil {
		return EvidenceResult{}, err
	}
	variant := strings.TrimSpace(opts.CoordinateVariant)
	if variant == "" {
		return EvidenceResult{}, errors.New("coordinate variant is required")
	}
	radius := opts.RadiusMeters
	if radius <= 0 {
		return EvidenceResult{}, errors.New("place evidence radius must be greater than 0")
	}
	if strings.TrimSpace(opts.OutputDir) == "" || strings.TrimSpace(opts.CacheDir) == "" {
		return EvidenceResult{}, errors.New("place evidence output and cache dirs are required")
	}
	if runner.callApple == nil {
		return EvidenceResult{}, errors.New("Apple evidence boundary is required")
	}
	if err := validateConfiguredGeoapify(opts.Geoapify); err != nil {
		return EvidenceResult{}, err
	}
	if err := os.MkdirAll(opts.OutputDir, 0o700); err != nil {
		return EvidenceResult{}, err
	}
	if err := os.MkdirAll(opts.CacheDir, 0o700); err != nil {
		return EvidenceResult{}, err
	}

	apple := captureAppleEvidence(ctx, opts, runner)
	reverse := captureGeoapifyReverse(ctx, opts)
	nearby := captureGeoapifyNearby(ctx, opts)
	captures := []*evidenceCapture{&apple, &reverse, &nearby}
	result := EvidenceResult{State: evidenceStateComplete, CoordinateVariant: variant}
	for index, capture := range captures {
		dirName := fmt.Sprintf("%02d-%s-%s-%s", index+1, safePathPart(capture.record.ProviderIdentity), safePathPart(capture.record.Operation), capture.record.CacheIdentity[:12])
		if err := writeEvidenceCapture(filepath.Join(opts.OutputDir, dirName), capture); err != nil {
			return EvidenceResult{}, err
		}
		result.Records = append(result.Records, capture.record)
		if capture.record.CompletionState == evidenceStateStopped {
			result.State = evidenceStateStopped
			result.StopReasons = append(result.StopReasons, capture.record.ProviderIdentity+" "+capture.record.Operation+": "+capture.record.StopReason)
			continue
		}
		if !capture.record.Cached {
			cacheCapture := *capture
			cacheCapture.record.RecordDir = ""
			if err := writeEvidenceCapture(filepath.Join(opts.CacheDir, capture.record.CacheIdentity), &cacheCapture); err != nil {
				return EvidenceResult{}, err
			}
		}
	}
	if result.State == evidenceStateStopped {
		return result, &EvidenceStoppedError{OutputDir: opts.OutputDir, StopReasons: append([]string(nil), result.StopReasons...)}
	}
	return result, nil
}

func completeCapture(input Input, provider, operation, variant, credentialReference string, request, response []byte, status int, parsed parsedEvidence) evidenceCapture {
	return evidenceCapture{
		record: EvidenceRecord{
			Input:               input,
			ProviderIdentity:    provider,
			Operation:           operation,
			CoordinateVariant:   variant,
			ParserVersion:       evidenceParserVersion,
			PreAuthRequestFile:  "request.raw",
			RawResponseFile:     "response.raw",
			HTTPStatus:          status,
			Address:             parsed.address,
			Candidates:          parsed.candidates,
			CompletionState:     evidenceStateComplete,
			CacheIdentity:       evidenceCacheIdentity(input, provider, operation, variant, credentialReference, request),
			CredentialReference: credentialReference,
		},
		request:  append([]byte(nil), request...),
		response: append([]byte(nil), response...),
	}
}

func stoppedCapture(input Input, provider, operation, variant, credentialReference string, request, response []byte, status int, parsed parsedEvidence, err error) evidenceCapture {
	capture := completeCapture(input, provider, operation, variant, credentialReference, request, response, status, parsed)
	capture.record.CompletionState = evidenceStateStopped
	if err != nil {
		capture.record.StopReason = err.Error()
	}
	return capture
}

func cachedCapture(cacheDir, provider, operation, variant, credentialReference string, request []byte, input Input, parser evidenceParser) (evidenceCapture, bool) {
	identity := evidenceCacheIdentity(input, provider, operation, variant, credentialReference, request)
	dir := filepath.Join(cacheDir, identity)
	metadata, err := os.ReadFile(filepath.Join(dir, "record.json"))
	if err != nil {
		return evidenceCapture{}, false
	}
	var record EvidenceRecord
	if err := json.Unmarshal(metadata, &record); err != nil {
		return evidenceCapture{}, false
	}
	storedRequest, err := os.ReadFile(filepath.Join(dir, "request.raw"))
	if err != nil || !bytes.Equal(storedRequest, request) {
		return evidenceCapture{}, false
	}
	response, err := readBoundedEvidenceFile(filepath.Join(dir, "response.raw"))
	if err != nil ||
		record.CompletionState != evidenceStateComplete ||
		record.CacheIdentity != identity ||
		record.ParserVersion != evidenceParserVersion ||
		record.ProviderIdentity != provider ||
		record.Operation != operation ||
		record.CoordinateVariant != variant ||
		record.CredentialReference != credentialReference ||
		!reflect.DeepEqual(record.Input, input) {
		return evidenceCapture{}, false
	}
	parsed, err := parser(response, record.HTTPStatus, input)
	if err != nil || !reflect.DeepEqual(parsed.address, record.Address) || !reflect.DeepEqual(parsed.candidates, record.Candidates) {
		return evidenceCapture{}, false
	}
	record.Cached = true
	record.CredentialReference = credentialReference
	return evidenceCapture{record: record, request: storedRequest, response: response}, true
}

func readBoundedEvidenceFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxRawEvidenceBytes+1))
	if err != nil {
		return data, err
	}
	if len(data) > maxRawEvidenceBytes {
		return data, fmt.Errorf("cached provider response exceeds %d bytes", maxRawEvidenceBytes)
	}
	return data, nil
}

func writeEvidenceCapture(dir string, capture *evidenceCapture) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "request.raw"), capture.request, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "response.raw"), capture.response, 0o600); err != nil {
		return err
	}
	capture.record.RecordDir = dir
	metadataRecord := capture.record
	metadataRecord.RecordDir = ""
	metadata, err := json.MarshalIndent(metadataRecord, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "record.json"), append(metadata, '\n'), 0o600)
}

func evidenceCacheIdentity(input Input, provider, operation, variant, credentialReference string, request []byte) string {
	hash := sha256.New()
	for _, value := range []string{provider, operation, evidenceParserVersion, variant, credentialReference} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	inputJSON, _ := json.Marshal(input)
	_, _ = hash.Write(inputJSON)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(request)
	return hex.EncodeToString(hash.Sum(nil))
}

func safePathPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteByte('-')
		}
	}
	if out.Len() == 0 {
		return "source"
	}
	return out.String()
}

func readBoundedResponse(response *http.Response) ([]byte, error) {
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRawEvidenceBytes+1))
	if err != nil {
		return data, err
	}
	if len(data) > maxRawEvidenceBytes {
		return data, errRawEvidenceTooLarge
	}
	return data, nil
}
