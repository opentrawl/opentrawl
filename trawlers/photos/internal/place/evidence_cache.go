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

func cachedCapture(cacheDir, provider, operation, variant, credentialReference string, selectionPolicy SelectionPolicy, request []byte, input Input, parser evidenceParser) (evidenceCapture, bool) {
	capture, found, err := checkedCachedCapture(cacheDir, provider, operation, variant, credentialReference, "", selectionPolicy, request, input, parser)
	if found && err != nil {
		return stoppedCapture(input, provider, operation, variant, credentialReference, selectionPolicy, request, nil, 0, parsedEvidence{}, err), true
	}
	return capture, found
}

func checkedCachedCapture(cacheDir, provider, operation, variant, credentialReference, credential string, selectionPolicy SelectionPolicy, request []byte, input Input, parser evidenceParser) (evidenceCapture, bool, error) {
	identity := evidenceCacheIdentity(input, provider, operation, variant, credentialReference, selectionPolicy, request)
	dir := filepath.Join(cacheDir, identity)
	if _, err := os.Lstat(dir); err != nil {
		if os.IsNotExist(err) {
			return evidenceCapture{}, false, nil
		}
		return evidenceCapture{}, true, fmt.Errorf("%w: inspect cache: %v", errEvidenceCacheIncomplete, err)
	}
	if err := ensurePrivateOutputRoot(dir); err != nil {
		return evidenceCapture{}, true, fmt.Errorf("%w: unsafe cache directory", errEvidenceCacheIncomplete)
	}
	recordPath := filepath.Join(dir, "record.json")
	if err := ensurePrivateInputFile(recordPath); err != nil {
		return evidenceCapture{}, true, fmt.Errorf("%w: unsafe cache record", errEvidenceCacheIncomplete)
	}
	metadata, err := os.ReadFile(recordPath)
	if err != nil {
		return evidenceCapture{}, true, fmt.Errorf("%w: read cache record", errEvidenceCacheIncomplete)
	}
	var record EvidenceRecord
	if err := json.Unmarshal(metadata, &record); err != nil {
		return evidenceCapture{}, true, fmt.Errorf("%w: parse cache record", errEvidenceCacheIncomplete)
	}
	if record.PreAuthRequestFile != "request.raw" || record.RawResponseFile != "response.raw" ||
		provider == appleEvidenceProvider && record.RawHeadersFile != "" || provider != appleEvidenceProvider && record.RawHeadersFile != "headers.raw" {
		return evidenceCapture{}, true, fmt.Errorf("%w: unsafe cache file name", errEvidenceCacheIncomplete)
	}
	for _, name := range []string{"request.raw", "response.raw", record.RawHeadersFile} {
		if name != "" {
			if err := ensurePrivateInputFile(filepath.Join(dir, name)); err != nil {
				return evidenceCapture{}, true, fmt.Errorf("%w: unsafe cache evidence", errEvidenceCacheIncomplete)
			}
		}
	}
	storedRequest, err := os.ReadFile(filepath.Join(dir, "request.raw"))
	if err != nil || !bytes.Equal(storedRequest, request) {
		return evidenceCapture{}, true, fmt.Errorf("%w: request mismatch", errEvidenceCacheIncomplete)
	}
	if record.StartedAt == "" || record.CompletedAt == "" {
		return evidenceCapture{}, true, fmt.Errorf("%w: timing is missing", errEvidenceCacheIncomplete)
	}
	response, responseErr := readBoundedEvidenceFile(filepath.Join(dir, "response.raw"))
	var headers []byte
	if record.RawHeadersFile != "" {
		headers, err = readBoundedEvidenceFile(filepath.Join(dir, "headers.raw"))
		if err != nil || record.RawHeadersSHA256 != evidenceDigest(headers) {
			return evidenceCapture{}, true, fmt.Errorf("%w: headers mismatch", errEvidenceCacheIncomplete)
		}
		status, statusErr := rawHTTPStatus(headers)
		if statusErr != nil || status != record.HTTPStatus {
			return evidenceCapture{}, true, fmt.Errorf("%w: status mismatch", errEvidenceCacheIncomplete)
		}
	}
	if responseContainsCredential(headers, credential) || responseContainsCredential(response, credential) {
		if err := discardCredentialCache(dir, record); err != nil {
			return evidenceCapture{}, true, fmt.Errorf("%w: discard unsafe cache", errEvidenceCacheIncomplete)
		}
		return evidenceCapture{}, true, errEvidenceCredential
	}
	if responseErr != nil || record.CompletionState != evidenceStateComplete || record.CacheIdentity != identity || record.ParserVersion != evidenceParserVersion ||
		record.PreAuthRequestSHA256 != evidenceDigest(storedRequest) || record.RawResponseSHA256 != evidenceDigest(response) || record.ProviderIdentity != provider ||
		record.Operation != operation || record.CoordinateVariant != variant || record.CredentialReference != credentialReference || !reflect.DeepEqual(selectionPolicyIntent(record.SelectionPolicy), selectionPolicyIntent(selectionPolicy)) || !validSelectionPolicy(record.SelectionPolicy, len(record.Candidates)) || !reflect.DeepEqual(record.Input, input) {
		return evidenceCapture{}, true, fmt.Errorf("%w: record mismatch", errEvidenceCacheIncomplete)
	}
	parsed, err := parser(response, record.HTTPStatus, input)
	if err != nil || !reflect.DeepEqual(parsed.address, record.Address) || !sameEvidenceCandidates(parsed.candidates, record.Candidates) {
		return evidenceCapture{}, true, fmt.Errorf("%w: parser mismatch", errEvidenceCacheIncomplete)
	}
	record.Candidates = parsed.candidates
	record.Cached = true
	return evidenceCapture{record: record, request: storedRequest, response: response, headers: headers}, true, nil
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

func campaignCachedCapture(opts EvidenceCampaignOptions, manifest *evidenceInventoryManifest, row *evidenceInventoryAsset, input Input, operation evidenceOperation) (evidenceCapture, bool, error) {
	request := inventoryRequestForOperation(row.Requests, operation)
	if request == nil {
		return evidenceCapture{}, true, fmt.Errorf("%w: %s inventory request", errEvidenceCacheIncomplete, operation)
	}
	parser := parseAppleEvidence
	credentialReference := ""
	selectionPolicy := SelectionPolicy{}
	if operation == evidenceOperationReverse {
		parser = parseGeoapifyEvidenceAtLimit(manifest.Provider.ReverseLimit)
		credentialReference = manifest.Provider.CredentialReference
		selectionPolicy.RequestedLimit = manifest.Provider.ReverseLimit
	}
	if operation == evidenceOperationNearby {
		parser = parseGeoapifyEvidenceAtLimit(manifest.Provider.NearbyLimit)
		credentialReference = manifest.Provider.CredentialReference
		selectionPolicy.RequestedLimit = manifest.Provider.NearbyLimit
	}
	return checkedCachedCapture(opts.CacheDir, request.Provider, request.Operation, evidenceCoordinateVariant, credentialReference, "", selectionPolicy, []byte(request.Bytes), input, parser)
}

func campaignCaseOperationCached(opts EvidenceCampaignOptions, manifest *evidenceInventoryManifest, index int, operation evidenceOperation) (bool, error) {
	row := inventoryAsset(manifest.Assets, manifest.Campaign.Cases[index].AssetID)
	if row == nil || row.Location == nil {
		return false, fmt.Errorf("mismatched")
	}
	input := Input{AssetID: row.AssetID, TakenAt: row.TakenAt, Location: *row.Location}
	_, found, err := campaignCachedCapture(opts, manifest, row, input, operation)
	return found, err
}

func campaignResultMatches(result EvidenceResult, manifest *evidenceInventoryManifest, row *evidenceInventoryAsset, input Input, operation evidenceOperation) bool {
	if len(result.Records) != 1 || result.CoordinateVariant != evidenceCoordinateVariant {
		return false
	}
	request := inventoryRequestForOperation(row.Requests, operation)
	if request == nil {
		return false
	}
	record := result.Records[0]
	credentialReference := ""
	if operation != evidenceOperationApple {
		credentialReference = manifest.Provider.CredentialReference
	}
	return record.ProviderIdentity == request.Provider && record.Operation == request.Operation && record.CoordinateVariant == evidenceCoordinateVariant &&
		record.PreAuthRequestSHA256 == request.SHA256 && record.CacheIdentity == request.CacheIdentity && record.CredentialReference == credentialReference && reflect.DeepEqual(record.Input, input)
}

func discardCredentialCache(dir string, record EvidenceRecord) error {
	for _, name := range []string{record.RawResponseFile, record.RawHeadersFile} {
		if name != "" {
			if err := writePrivateFile(filepath.Join(dir, name), nil); err != nil {
				return err
			}
		}
	}
	marker, err := json.MarshalIndent(struct {
		CompletionState string `json:"completion_state"`
		StopReason      string `json:"stop_reason"`
	}{evidenceStateStopped, evidenceStopCredential}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(dir, "record.json"), append(marker, '\n'))
}

func evidenceDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func evidenceRandomDigest(snapshotID, assetID string) string {
	digest := sha256.Sum256([]byte("place-evidence-random-v1\x00" + snapshotID + "\x00" + assetID))
	return hex.EncodeToString(digest[:])
}

func marshalEvidenceManifest(manifest evidenceInventoryManifest) (string, []byte, error) {
	manifest.ManifestDigest = ""
	payload, err := json.Marshal(manifest)
	if err != nil {
		return "", nil, err
	}
	digest := evidenceDigest(payload)
	manifest.ManifestDigest = digest
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", nil, err
	}
	return digest, append(data, '\n'), nil
}

func readEvidenceManifest(path string) (evidenceInventoryManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return evidenceInventoryManifest{}, err
	}
	var manifest evidenceInventoryManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return evidenceInventoryManifest{}, err
	}
	want := manifest.ManifestDigest
	digest, _, err := marshalEvidenceManifest(manifest)
	if err != nil || want == "" || digest != want {
		return evidenceInventoryManifest{}, fmt.Errorf("place evidence manifest digest is invalid")
	}
	if manifest.Version != evidenceInventoryVersion || manifest.State != inventoryStateComplete {
		return evidenceInventoryManifest{}, fmt.Errorf("place evidence inventory is incomplete")
	}
	return manifest, nil
}

func saveEvidenceManifest(path string, manifest *evidenceInventoryManifest) error {
	_, data, err := marshalEvidenceManifest(*manifest)
	if err != nil {
		return err
	}
	if err := writePrivateFile(path, data); err != nil {
		return err
	}
	return json.Unmarshal(data, manifest)
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
	if err := ensurePrivateEvidenceDirectory(dir); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"request.raw": capture.request, capture.record.RawResponseFile: capture.response, capture.record.RawHeadersFile: capture.headers} {
		if name != "" {
			if err := writePrivateFile(filepath.Join(dir, name), data); err != nil {
				return err
			}
		}
	}
	capture.record.RecordDir = dir
	metadataRecord := capture.record
	metadataRecord.RecordDir = ""
	metadata, err := json.MarshalIndent(metadataRecord, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(dir, "record.json"), append(metadata, '\n'))
}

func ensurePrivateEvidenceDirectory(path string) error {
	clean := filepath.Clean(path)
	if _, err := os.Lstat(clean); err == nil {
		return ensurePrivateOutputRoot(clean)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(clean)
	if parent == clean {
		return fmt.Errorf("private evidence directory has no safe parent")
	}
	if err := ensurePrivateEvidenceDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(clean, 0o700); err != nil {
		return err
	}
	return ensurePrivateOutputRoot(clean)
}

func attachRawHeaders(capture evidenceCapture, headers []byte) evidenceCapture {
	capture.headers = append([]byte(nil), headers...)
	capture.record.RawHeadersFile = "headers.raw"
	capture.record.RawHeadersSHA256 = evidenceDigest(headers)
	return capture
}

func discardedCredentialCapture(input Input, provider, operation, variant, credentialReference string, selectionPolicy SelectionPolicy, request []byte, status int) evidenceCapture {
	capture := stoppedCapture(input, provider, operation, variant, credentialReference, selectionPolicy, request, nil, status, parsedEvidence{}, errEvidenceCredential)
	capture.record.RawResponseFile, capture.record.RawResponseSHA256 = "", ""
	capture.response = nil
	return capture
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

func digestCampaignCanaries(outputDir string, cases []evidenceCampaignCase) (string, error) {
	hash := sha256.New()
	count := 0
	for index, campaignCase := range cases {
		if !campaignCase.Canary {
			continue
		}
		root := filepath.Join(outputDir, "cases", fmt.Sprintf("%04d", index+1), campaignPhaseCanary)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("canary evidence contains a symlink")
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			if err := ensurePrivateInputFile(path); err != nil {
				return err
			}
			data, err := readBoundedEvidenceFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(outputDir, path)
			if err != nil {
				return err
			}
			_, _ = hash.Write([]byte(relative))
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write(data)
			_, _ = hash.Write([]byte{0})
			count++
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	if count == 0 {
		return "", fmt.Errorf("canary evidence is missing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateCanaryInspectionReceipt(path, manifestDigest, expectedEvidenceDigest string) (string, error) {
	if err := ensurePrivateInputFile(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var receipt struct {
		ManifestDigest       string `json:"manifest_digest"`
		CanaryEvidenceDigest string `json:"canary_evidence_digest"`
		Inspected            bool   `json:"inspected"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return "", fmt.Errorf("malformed")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || !receipt.Inspected || receipt.ManifestDigest != manifestDigest || receipt.CanaryEvidenceDigest != expectedEvidenceDigest {
		return "", fmt.Errorf("mismatched")
	}
	return evidenceDigest(data), nil
}
