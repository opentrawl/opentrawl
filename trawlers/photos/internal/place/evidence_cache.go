package place

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

var ErrCheckedEvidenceUnavailable = errors.New("checked place evidence is unavailable")

// CheckedEvidenceCacheIdentity is the stable cache key for one producer's
// exact input, operation policy and pre-authenticated request bytes.
func CheckedEvidenceCacheIdentity(input Input, operation CheckedOperation, request []byte) string {
	return evidenceCacheIdentity(input, operation.ProviderIdentity, operation.Operation, operation.CoordinateVariant, operation.CredentialReference, operation.SelectionPolicy, request)
}

// LoadCheckedEvidence reopens only complete records that match exact source
// facts and the caller's ordered operation policy. It never builds a request,
// contacts a provider or writes a cache entry.
func LoadCheckedEvidence(cacheDir string, input Input, operations []CheckedOperation) ([]EvidenceRecord, error) {
	if err := validateInput(input); err != nil {
		return nil, fmt.Errorf("%w: source facts", ErrCheckedEvidenceUnavailable)
	}
	if err := validateCheckedOperations(operations); err != nil {
		return nil, fmt.Errorf("%w: operation policy", ErrCheckedEvidenceUnavailable)
	}
	if err := ensurePrivateOutputRoot(cacheDir); err != nil {
		return nil, fmt.Errorf("%w: cache root", ErrCheckedEvidenceUnavailable)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("%w: read cache", ErrCheckedEvidenceUnavailable)
	}
	records := make(map[string]checkedEvidenceRecord, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return nil, fmt.Errorf("%w: unsafe cache entry", ErrCheckedEvidenceUnavailable)
		}
		record, err := readCheckedEvidenceRecord(filepath.Join(cacheDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		key := checkedOperationKey(CheckedOperation{
			ProviderIdentity: record.record.ProviderIdentity, Operation: record.record.Operation,
			CoordinateVariant: record.record.CoordinateVariant, CredentialReference: record.record.CredentialReference,
			SelectionPolicy: record.record.SelectionPolicy,
		})
		if _, exists := records[key]; exists {
			return nil, fmt.Errorf("%w: duplicate operation", ErrCheckedEvidenceUnavailable)
		}
		records[key] = record
	}
	loaded := make([]EvidenceRecord, 0, len(operations))
	for _, operation := range operations {
		cached, found := records[checkedOperationKey(operation)]
		if !found || !reflect.DeepEqual(cached.record.Input, input) {
			return nil, fmt.Errorf("%w: required operation", ErrCheckedEvidenceUnavailable)
		}
		address, candidates, err := operation.Parser(cached.response, cached.record.HTTPStatus, input)
		if err != nil || !reflect.DeepEqual(address, cached.record.Address) || !sameEvidenceCandidates(candidates, cached.record.Candidates) {
			return nil, fmt.Errorf("%w: parsed evidence mismatch", ErrCheckedEvidenceUnavailable)
		}
		cached.record.Candidates = candidates
		loaded = append(loaded, cached.record)
	}
	return loaded, nil
}

func validateCheckedOperations(operations []CheckedOperation) error {
	if len(operations) == 0 {
		return errors.New("no operations")
	}
	seen := map[string]bool{}
	for _, operation := range operations {
		if strings.TrimSpace(operation.ProviderIdentity) == "" || strings.TrimSpace(operation.Operation) == "" || strings.TrimSpace(operation.CoordinateVariant) == "" || operation.Parser == nil || !validSelectionPolicy(operation.SelectionPolicy, -1) {
			return errors.New("incomplete operation")
		}
		key := checkedOperationKey(operation)
		if seen[key] {
			return errors.New("duplicate operation")
		}
		seen[key] = true
	}
	return nil
}

func checkedOperationKey(operation CheckedOperation) string {
	policy, _ := json.Marshal(selectionPolicyIntent(operation.SelectionPolicy))
	return strings.Join([]string{operation.ProviderIdentity, operation.Operation, operation.CoordinateVariant, operation.CredentialReference, string(policy)}, "\x00")
}

func selectionPolicyIntent(policy SelectionPolicy) SelectionPolicy {
	policy.LimitReached = false
	policy.MoreResultsNotRequested = false
	return policy
}

func validSelectionPolicy(policy SelectionPolicy, candidateCount int) bool {
	if policy.RequestedLimit == 0 {
		return !policy.LimitReached && !policy.MoreResultsNotRequested && !policy.BoundedReverse
	}
	if policy.RequestedLimit < 1 {
		return false
	}
	if candidateCount < 0 {
		return !policy.LimitReached && !policy.MoreResultsNotRequested
	}
	if candidateCount > policy.RequestedLimit || policy.LimitReached != (candidateCount == policy.RequestedLimit) {
		return false
	}
	return policy.MoreResultsNotRequested == (policy.BoundedReverse && policy.LimitReached)
}

type checkedEvidenceRecord struct {
	record   EvidenceRecord
	response []byte
}

func readCheckedEvidenceRecord(dir string) (checkedEvidenceRecord, error) {
	if err := ensurePrivateOutputRoot(dir); err != nil {
		return checkedEvidenceRecord{}, fmt.Errorf("%w: unsafe cache directory", ErrCheckedEvidenceUnavailable)
	}
	recordPath := filepath.Join(dir, "record.json")
	if err := ensurePrivateInputFile(recordPath); err != nil {
		return checkedEvidenceRecord{}, fmt.Errorf("%w: unsafe cache record", ErrCheckedEvidenceUnavailable)
	}
	metadata, err := os.ReadFile(recordPath)
	if err != nil {
		return checkedEvidenceRecord{}, fmt.Errorf("%w: read cache record", ErrCheckedEvidenceUnavailable)
	}
	var record EvidenceRecord
	if json.Unmarshal(metadata, &record) != nil || record.PreAuthRequestFile != "request.raw" || record.RawResponseFile != "response.raw" ||
		(record.ProviderIdentity == appleEvidenceProvider && record.RawHeadersFile != "") || (record.ProviderIdentity != appleEvidenceProvider && record.RawHeadersFile != "headers.raw") {
		return checkedEvidenceRecord{}, fmt.Errorf("%w: malformed cache record", ErrCheckedEvidenceUnavailable)
	}
	for _, name := range []string{"request.raw", "response.raw", record.RawHeadersFile} {
		if name != "" {
			if err := ensurePrivateInputFile(filepath.Join(dir, name)); err != nil {
				return checkedEvidenceRecord{}, fmt.Errorf("%w: unsafe cache evidence", ErrCheckedEvidenceUnavailable)
			}
		}
	}
	request, requestErr := readBoundedEvidenceFile(filepath.Join(dir, "request.raw"))
	response, responseErr := readBoundedEvidenceFile(filepath.Join(dir, "response.raw"))
	if requestErr != nil || responseErr != nil || record.CompletionState != evidenceStateComplete || record.ParserVersion != evidenceParserVersion ||
		record.StopReason != "" || record.StopDetail != "" || record.ProviderErrorClass != "" || strings.TrimSpace(record.ProviderIdentity) == "" || strings.TrimSpace(record.Operation) == "" || strings.TrimSpace(record.CoordinateVariant) == "" ||
		record.PreAuthRequestSHA256 != evidenceDigest(request) || record.RawResponseSHA256 != evidenceDigest(response) ||
		record.CacheIdentity != evidenceCacheIdentity(record.Input, record.ProviderIdentity, record.Operation, record.CoordinateVariant, record.CredentialReference, record.SelectionPolicy, request) ||
		!validSelectionPolicy(record.SelectionPolicy, len(record.Candidates)) || (record.Address == nil && len(record.Candidates) == 0) ||
		(record.HTTPStatus != 0 && (record.HTTPStatus < 200 || record.HTTPStatus >= 300)) {
		return checkedEvidenceRecord{}, fmt.Errorf("%w: cache record mismatch", ErrCheckedEvidenceUnavailable)
	}
	if record.RawHeadersFile != "" {
		headers, err := readBoundedEvidenceFile(filepath.Join(dir, record.RawHeadersFile))
		status, statusErr := rawHTTPStatus(headers)
		if err != nil || statusErr != nil || record.RawHeadersSHA256 != evidenceDigest(headers) || status != record.HTTPStatus {
			return checkedEvidenceRecord{}, fmt.Errorf("%w: cache headers mismatch", ErrCheckedEvidenceUnavailable)
		}
	}
	if filepath.Base(dir) != record.CacheIdentity || record.StartedAt == "" || record.CompletedAt == "" || record.DurationMilliseconds < 0 {
		return checkedEvidenceRecord{}, fmt.Errorf("%w: cache record timing", ErrCheckedEvidenceUnavailable)
	}
	return checkedEvidenceRecord{record: record, response: response}, nil
}

func sameEvidenceCandidates(left, right []EvidenceCandidate) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		leftCandidate := left[index]
		rightCandidate := right[index]
		leftCandidate.ProviderResult = nil
		rightCandidate.ProviderResult = nil
		if !reflect.DeepEqual(leftCandidate, rightCandidate) {
			return false
		}
	}
	return true
}

func rawHTTPStatus(headers []byte) (int, error) {
	line, _, found := bytes.Cut(headers, []byte("\r\n"))
	fields := bytes.Fields(line)
	if !found || len(fields) < 2 || !bytes.HasPrefix(fields[0], []byte("HTTP/")) {
		return 0, fmt.Errorf("malformed HTTP status")
	}
	return strconv.Atoi(string(fields[1]))
}

func evidenceDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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

func evidenceCacheIdentity(input Input, provider, operation, variant, credentialReference string, selectionPolicy SelectionPolicy, request []byte) string {
	hash := sha256.New()
	for _, value := range []string{provider, operation, evidenceParserVersion, variant, credentialReference} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	inputJSON, _ := json.Marshal(input)
	_, _ = hash.Write(inputJSON)
	_, _ = hash.Write([]byte{0})
	selectionJSON, _ := json.Marshal(selectionPolicyIntent(selectionPolicy))
	_, _ = hash.Write(selectionJSON)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(request)
	return hex.EncodeToString(hash.Sum(nil))
}
