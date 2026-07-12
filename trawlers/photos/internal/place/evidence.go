package place

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	evidenceParserVersion       = "photos-place-evidence-v2"
	evidenceStateComplete       = "complete"
	evidenceStateStopped        = "stopped"
	evidenceStopEmpty           = "empty"
	evidenceStopFailed          = "failed"
	evidenceStopMalformed       = "malformed"
	evidenceStopNoResult        = "no_result"
	evidenceStopSaturated       = "limit_saturated"
	evidenceStopTooLarge        = "response_too_large"
	evidenceStopBilling         = "billing_signal"
	evidenceStopCredential      = "credential_bearing_response"
	evidenceStopRateLimited     = "rate_limited"
	evidenceStopCacheIncomplete = "cache_incomplete"
	appleEmptyResponse          = "Apple returned an empty response"
	maxRawEvidenceBytes         = 4 << 20
)

var (
	errEvidenceEmpty           = errors.New(evidenceStopEmpty)
	errEvidenceFailed          = errors.New(evidenceStopFailed)
	errEvidenceMalformed       = errors.New(evidenceStopMalformed)
	errEvidenceSaturated       = errors.New(evidenceStopSaturated)
	errEvidenceBilling         = errors.New(evidenceStopBilling)
	errEvidenceCredential      = errors.New(evidenceStopCredential)
	errEvidenceRateLimited     = errors.New(evidenceStopRateLimited)
	errEvidenceCacheIncomplete = errors.New(evidenceStopCacheIncomplete)
	errRawEvidenceTooLarge     = fmt.Errorf("provider response exceeds %d bytes", maxRawEvidenceBytes)
)

type EvidenceOptions struct {
	Input             Input
	CoordinateVariant string
	RadiusMeters      float64
	OutputDir         string
	CacheDir          string
	Operation         EvidenceOperation
	Geoapify          ConfiguredGeoapifyEvidence
}

type EvidenceOperation string

const (
	EvidenceOperationAll             EvidenceOperation = "all"
	EvidenceOperationApple           EvidenceOperation = "apple"
	EvidenceOperationGeoapifyReverse EvidenceOperation = "geoapify-reverse"
	EvidenceOperationGeoapifyNearby  EvidenceOperation = "geoapify-nearby"
)

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
	Input                Input               `json:"input"`
	ProviderIdentity     string              `json:"provider_identity"`
	Operation            string              `json:"operation"`
	CoordinateVariant    string              `json:"coordinate_variant"`
	ParserVersion        string              `json:"parser_version"`
	PreAuthRequestFile   string              `json:"pre_auth_request_file"`
	PreAuthRequestSHA256 string              `json:"pre_auth_request_sha256"`
	RawResponseFile      string              `json:"raw_response_file"`
	RawResponseSHA256    string              `json:"raw_response_sha256"`
	RawHeadersFile       string              `json:"raw_headers_file,omitempty"`
	RawHeadersSHA256     string              `json:"raw_headers_sha256,omitempty"`
	HTTPStatus           int                 `json:"http_status,omitempty"`
	Address              *Address            `json:"address,omitempty"`
	Candidates           []EvidenceCandidate `json:"candidates"`
	CompletionState      string              `json:"completion_state"`
	StopReason           string              `json:"stop_reason,omitempty"`
	StopDetail           string              `json:"stop_detail,omitempty"`
	ProviderErrorClass   string              `json:"provider_error_class,omitempty"`
	CacheIdentity        string              `json:"cache_identity"`
	Cached               bool                `json:"cached,omitempty"`
	RecordDir            string              `json:"record_dir,omitempty"`
	CredentialReference  string              `json:"credential_reference,omitempty"`
	StartedAt            string              `json:"started_at"`
	CompletedAt          string              `json:"completed_at"`
	DurationMilliseconds float64             `json:"duration_milliseconds"`
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
	headers  []byte
}

type parsedEvidence struct {
	address    *Address
	candidates []EvidenceCandidate
}

type evidenceParser func([]byte, int, Input) (parsedEvidence, error)

type evidenceRunner struct {
	callApple func(context.Context, Input, float64) appleBoundaryOutput
	now       func() time.Time
}

type evidenceOperation string

const (
	evidenceOperationApple   evidenceOperation = "apple"
	evidenceOperationReverse evidenceOperation = "geoapify_reverse"
	evidenceOperationNearby  evidenceOperation = "geoapify_nearby"
)

func LoadEvidenceInput(path string) (Input, error) {
	return loadInput(path)
}

func RunEvidence(ctx context.Context, opts EvidenceOptions) (EvidenceResult, error) {
	selected, err := selectedEvidenceOperations(opts.Operation)
	if err != nil {
		return EvidenceResult{}, err
	}
	return runEvidenceOperations(ctx, opts, evidenceRunner{
		callApple: callAppleBoundary,
		now:       time.Now,
	}, selected)
}

func runEvidence(ctx context.Context, opts EvidenceOptions, runner evidenceRunner) (EvidenceResult, error) {
	return runEvidenceOperations(ctx, opts, runner, []evidenceOperation{
		evidenceOperationApple,
		evidenceOperationReverse,
		evidenceOperationNearby,
	})
}

func ParseEvidenceOperation(value string) (EvidenceOperation, error) {
	operation := EvidenceOperation(strings.TrimSpace(value))
	if operation == "" {
		operation = EvidenceOperationAll
	}
	if _, err := selectedEvidenceOperations(operation); err != nil {
		return "", err
	}
	return operation, nil
}

func selectedEvidenceOperations(operation EvidenceOperation) ([]evidenceOperation, error) {
	switch operation {
	case "", EvidenceOperationAll:
		return []evidenceOperation{evidenceOperationApple, evidenceOperationReverse, evidenceOperationNearby}, nil
	case EvidenceOperationApple:
		return []evidenceOperation{evidenceOperationApple}, nil
	case EvidenceOperationGeoapifyReverse:
		return []evidenceOperation{evidenceOperationReverse}, nil
	case EvidenceOperationGeoapifyNearby:
		return []evidenceOperation{evidenceOperationNearby}, nil
	default:
		return nil, fmt.Errorf("unknown place evidence operation %q", operation)
	}
}

func runEvidenceOperations(ctx context.Context, opts EvidenceOptions, runner evidenceRunner, selected []evidenceOperation) (EvidenceResult, error) {
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

	if runner.now == nil {
		runner.now = time.Now
	}
	captures := make([]evidenceCapture, 0, len(selected))
	result := EvidenceResult{State: evidenceStateComplete, CoordinateVariant: variant}
	for index, operation := range selected {
		started := runner.now().UTC()
		var capture evidenceCapture
		switch operation {
		case evidenceOperationApple:
			capture = captureAppleEvidence(ctx, opts, runner)
		case evidenceOperationReverse:
			capture = captureGeoapifyReverse(ctx, opts)
		case evidenceOperationNearby:
			capture = captureGeoapifyNearby(ctx, opts)
		default:
			return EvidenceResult{}, fmt.Errorf("unknown place evidence operation %q", operation)
		}
		completed := runner.now().UTC()
		capture.record.StartedAt = started.Format(time.RFC3339Nano)
		capture.record.CompletedAt = completed.Format(time.RFC3339Nano)
		capture.record.DurationMilliseconds = float64(completed.Sub(started)) / float64(time.Millisecond)
		dirName := fmt.Sprintf("%02d-%s-%s-%s", index+1, safePathPart(capture.record.ProviderIdentity), safePathPart(capture.record.Operation), capture.record.CacheIdentity[:12])
		if err := writeEvidenceCapture(filepath.Join(opts.OutputDir, dirName), &capture); err != nil {
			return EvidenceResult{}, err
		}
		result.Records = append(result.Records, capture.record)
		if capture.record.CompletionState == evidenceStateStopped {
			result.State = evidenceStateStopped
			result.StopReasons = append(result.StopReasons, capture.record.ProviderIdentity+" "+capture.record.Operation+": "+capture.record.StopReason)
			return result, &EvidenceStoppedError{OutputDir: opts.OutputDir, StopReasons: append([]string(nil), result.StopReasons...)}
		}
		captures = append(captures, capture)
	}
	for _, capture := range captures {
		if capture.record.Cached {
			continue
		}
		cacheCapture := capture
		cacheCapture.record.RecordDir = ""
		if err := writeEvidenceCapture(filepath.Join(opts.CacheDir, capture.record.CacheIdentity), &cacheCapture); err != nil {
			return EvidenceResult{}, err
		}
	}
	return result, nil
}

func completeCapture(input Input, provider, operation, variant, credentialReference string, request, response []byte, status int, parsed parsedEvidence) evidenceCapture {
	return evidenceCapture{
		record: EvidenceRecord{
			Input:                input,
			ProviderIdentity:     provider,
			Operation:            operation,
			CoordinateVariant:    variant,
			ParserVersion:        evidenceParserVersion,
			PreAuthRequestFile:   "request.raw",
			PreAuthRequestSHA256: evidenceDigest(request),
			RawResponseFile:      "response.raw",
			RawResponseSHA256:    evidenceDigest(response),
			HTTPStatus:           status,
			Address:              parsed.address,
			Candidates:           parsed.candidates,
			CompletionState:      evidenceStateComplete,
			CacheIdentity:        evidenceCacheIdentity(input, provider, operation, variant, credentialReference, request),
			CredentialReference:  credentialReference,
		},
		request:  append([]byte(nil), request...),
		response: append([]byte(nil), response...),
	}
}

func stoppedCapture(input Input, provider, operation, variant, credentialReference string, request, response []byte, status int, parsed parsedEvidence, err error) evidenceCapture {
	capture := completeCapture(input, provider, operation, variant, credentialReference, request, response, status, parsed)
	capture.record.CompletionState = evidenceStateStopped
	capture.record.StopReason = namedEvidenceStop(response, err)
	capture.record.ProviderErrorClass = evidenceErrorClass(err)
	if err != nil && err.Error() != capture.record.StopReason {
		capture.record.StopDetail = err.Error()
	}
	return capture
}

func evidenceErrorClass(err error) string {
	switch {
	case errors.Is(err, ErrProviderThrottled):
		return "throttled"
	case errors.Is(err, ErrProviderTimeout):
		return "timeout"
	case errors.Is(err, ErrProviderNoResult):
		return "no_result"
	case errors.Is(err, errEvidenceRateLimited):
		return "rate_limited"
	case errors.Is(err, errEvidenceBilling):
		return "billing"
	case err != nil:
		return "other"
	default:
		return ""
	}
}

func namedEvidenceStop(response []byte, err error) string {
	switch {
	case errors.Is(err, errEvidenceBilling):
		return evidenceStopBilling
	case errors.Is(err, errEvidenceCredential):
		return evidenceStopCredential
	case errors.Is(err, errEvidenceRateLimited):
		return evidenceStopRateLimited
	case errors.Is(err, errEvidenceCacheIncomplete):
		return evidenceStopCacheIncomplete
	case errors.Is(err, errEvidenceSaturated):
		return evidenceStopSaturated
	case errors.Is(err, ErrProviderNoResult):
		return evidenceStopNoResult
	case errors.Is(err, errRawEvidenceTooLarge):
		return evidenceStopTooLarge
	case errors.Is(err, errEvidenceMalformed):
		return evidenceStopMalformed
	case errors.Is(err, errEvidenceEmpty):
		return evidenceStopEmpty
	case err != nil && err.Error() == appleEmptyResponse:
		return evidenceStopEmpty
	case errors.Is(err, errEvidenceFailed):
		return evidenceStopFailed
	}
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &syntaxError) || errors.As(err, &typeError) {
		return evidenceStopMalformed
	}
	if err != nil {
		return evidenceStopFailed
	}
	if len(response) == 0 {
		return evidenceStopEmpty
	}
	return evidenceStopFailed
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
