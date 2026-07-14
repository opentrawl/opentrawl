package place

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCheckedEvidenceAcceptsBoundedReverseOnly(t *testing.T) {
	input := syntheticEvidenceInput(52.36, 4.89)
	operation := CheckedOperation{
		ProviderIdentity:  "synthetic-reverse",
		Operation:         "reverse",
		CoordinateVariant: "source-coordinate",
		SelectionPolicy: SelectionPolicy{
			RequestedLimit: 1, BoundedReverse: true,
		},
		Parser: syntheticCheckedEvidenceParser,
	}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSyntheticCheckedEvidence(t, cacheDir, input, operation)

	records, err := LoadCheckedEvidence(cacheDir, input, []CheckedOperation{operation})
	if err != nil || len(records) != 1 || !records[0].SelectionPolicy.LimitReached || !records[0].SelectionPolicy.MoreResultsNotRequested {
		t.Fatalf("loaded records=%#v error=%v", records, err)
	}

	nearby := operation
	nearby.Operation = "nearby"
	nearby.SelectionPolicy.BoundedReverse = false
	if _, err := LoadCheckedEvidence(cacheDir, input, []CheckedOperation{nearby}); !errors.Is(err, ErrCheckedEvidenceUnavailable) {
		t.Fatalf("saturated nearby evidence error=%v", err)
	}
}

func TestLoadCheckedEvidenceRejectsPolicyPayloadAndParserTampering(t *testing.T) {
	input := syntheticEvidenceInput(52.36, 4.89)
	operation := CheckedOperation{
		ProviderIdentity: "synthetic-reverse", Operation: "reverse", CoordinateVariant: "source-coordinate",
		SelectionPolicy: SelectionPolicy{RequestedLimit: 1, BoundedReverse: true},
		Parser:          syntheticCheckedEvidenceParser,
	}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	recordDir := writeSyntheticCheckedEvidence(t, cacheDir, input, operation)
	for _, mutate := range []func(*EvidenceRecord){
		func(record *EvidenceRecord) { record.ParserVersion = "photos-place-evidence-v2" },
		func(record *EvidenceRecord) { record.SelectionPolicy.RequestedLimit = 2 },
		func(record *EvidenceRecord) { record.Address.Formatted = "Tampered Place" },
		func(record *EvidenceRecord) { record.Candidates[0].Name = "Tampered Place" },
	} {
		data, err := os.ReadFile(filepath.Join(recordDir, "record.json"))
		if err != nil {
			t.Fatal(err)
		}
		var record EvidenceRecord
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		mutate(&record)
		data, err = json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(recordDir, "record.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCheckedEvidence(cacheDir, input, []CheckedOperation{operation}); !errors.Is(err, ErrCheckedEvidenceUnavailable) {
			t.Fatalf("tampered record error=%v", err)
		}
		recordDir = writeSyntheticCheckedEvidence(t, cacheDir, input, operation)
	}
	if err := os.WriteFile(filepath.Join(recordDir, "response.raw"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCheckedEvidence(cacheDir, input, []CheckedOperation{operation}); !errors.Is(err, ErrCheckedEvidenceUnavailable) {
		t.Fatalf("tampered payload error=%v", err)
	}
}

func syntheticCheckedEvidenceParser(_ []byte, _ int, _ Input) (*Address, []EvidenceCandidate, error) {
	return &Address{Formatted: "Synthetic Place", Source: "synthetic"}, []EvidenceCandidate{{ProviderIndex: 0, ProviderID: "synthetic-place", Name: "Synthetic Place", Source: "synthetic"}}, nil
}

func writeSyntheticCheckedEvidence(t *testing.T, cacheDir string, input Input, operation CheckedOperation) string {
	t.Helper()
	request := []byte("GET /synthetic-reverse")
	response := []byte(`{"synthetic":"place"}`)
	capture := completeCapture(input, operation.ProviderIdentity, operation.Operation, operation.CoordinateVariant, operation.CredentialReference, operation.SelectionPolicy, request, response, 200, parsedEvidence{
		address:    &Address{Formatted: "Synthetic Place", Source: "synthetic"},
		candidates: []EvidenceCandidate{{ProviderIndex: 0, ProviderID: "synthetic-place", Name: "Synthetic Place", Source: "synthetic"}},
	})
	capture.record.SelectionPolicy.LimitReached = true
	capture.record.SelectionPolicy.MoreResultsNotRequested = true
	capture.record.StartedAt = "2026-01-01T00:00:00Z"
	capture.record.CompletedAt = "2026-01-01T00:00:00Z"
	capture = attachRawHeaders(capture, []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"))
	dir := filepath.Join(cacheDir, capture.record.CacheIdentity)
	if err := writeEvidenceCapture(dir, &capture); err != nil {
		t.Fatal(err)
	}
	return dir
}
