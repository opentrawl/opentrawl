package imagemetadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	recordFilename     = "record.json"
	projectionFilename = "projection.json"
	proofFilename      = "proof.json"
)

type Extractor func(context.Context, string) ([]byte, error)

type Store struct {
	root    string
	extract Extractor
}

func NewStore(root string, extract Extractor) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("image metadata cache path is required")
	}
	if extract == nil {
		return nil, errors.New("image metadata extractor is required")
	}
	if err := privateDirectory(root); err != nil {
		return nil, fmt.Errorf("create image metadata cache: %w", err)
	}
	return &Store{root: root, extract: extract}, nil
}

func (s *Store) Load(ctx context.Context, sourcePath, provedSHA256 string) (Artifacts, error) {
	if s == nil {
		return Artifacts{}, errors.New("image metadata store is not configured")
	}
	provedSHA256 = strings.ToLower(strings.TrimSpace(provedSHA256))
	if err := validateSHA256(provedSHA256); err != nil {
		return Artifacts{}, fmt.Errorf("proved original SHA-256: %w", err)
	}
	actualSHA256, err := fileSHA256(ctx, sourcePath)
	if err != nil {
		return Artifacts{}, fmt.Errorf("hash exact original: %w", err)
	}
	if actualSHA256 != provedSHA256 {
		return Artifacts{}, fmt.Errorf("exact original SHA-256 mismatch: got %s, want %s", actualSHA256, provedSHA256)
	}

	dir := filepath.Join(s.root, provedSHA256, ExtractorVersion)
	if cached, ok := readCheckedArtifacts(dir, provedSHA256); ok {
		cached.CacheHit = true
		return cached, nil
	}

	raw, err := s.extract(ctx, sourcePath)
	if err != nil {
		return Artifacts{}, fmt.Errorf("extract ImageIO metadata: %w", err)
	}
	var record Record
	if err := decodeJSON(raw, &record); err != nil {
		return Artifacts{}, fmt.Errorf("decode ImageIO metadata: %w", err)
	}
	record.ExtractorVersion = ExtractorVersion
	record.OriginalSHA256 = provedSHA256
	if err := record.validate(); err != nil {
		return Artifacts{}, fmt.Errorf("validate ImageIO metadata: %w", err)
	}
	projection := Project(record)
	if len(projection.Lines) == 0 {
		return Artifacts{}, errors.New("ImageIO metadata projection is empty")
	}

	recordBytes, err := canonicalJSON(record)
	if err != nil {
		return Artifacts{}, err
	}
	projectionBytes, err := canonicalJSON(projection)
	if err != nil {
		return Artifacts{}, err
	}
	proof := Proof{
		ExtractorVersion: ExtractorVersion,
		OriginalSHA256:   provedSHA256,
		RecordSHA256:     bytesSHA256(recordBytes),
		ProjectionSHA256: bytesSHA256(projectionBytes),
		FieldCount:       countFields(record),
		RenderedCount:    projection.RenderedFieldCount,
		ExclusionCount:   len(projection.Exclusions),
	}
	if proof.FieldCount != proof.RenderedCount+proof.ExclusionCount {
		return Artifacts{}, fmt.Errorf("projection coverage mismatch: %d fields, %d rendered, %d excluded", proof.FieldCount, proof.RenderedCount, proof.ExclusionCount)
	}
	proofBytes, err := canonicalJSON(proof)
	if err != nil {
		return Artifacts{}, err
	}
	if err := privateDirectory(dir); err != nil {
		return Artifacts{}, err
	}
	for _, artifact := range []struct {
		name string
		data []byte
	}{
		{recordFilename, recordBytes},
		{projectionFilename, projectionBytes},
		{proofFilename, proofBytes},
	} {
		if err := writeAtomic(filepath.Join(dir, artifact.name), artifact.data); err != nil {
			return Artifacts{}, err
		}
	}
	return Artifacts{Record: record, Projection: projection, Proof: proof}, nil
}

func readCheckedArtifacts(dir, originalSHA256 string) (Artifacts, bool) {
	if !privateCacheDirectory(dir) {
		return Artifacts{}, false
	}
	for _, name := range []string{recordFilename, projectionFilename, proofFilename} {
		if !privateRegularFile(filepath.Join(dir, name)) {
			return Artifacts{}, false
		}
	}
	recordBytes, err := os.ReadFile(filepath.Join(dir, recordFilename))
	if err != nil {
		return Artifacts{}, false
	}
	projectionBytes, err := os.ReadFile(filepath.Join(dir, projectionFilename))
	if err != nil {
		return Artifacts{}, false
	}
	proofBytes, err := os.ReadFile(filepath.Join(dir, proofFilename))
	if err != nil {
		return Artifacts{}, false
	}
	var artifacts Artifacts
	if err := decodeJSON(recordBytes, &artifacts.Record); err != nil {
		return Artifacts{}, false
	}
	if err := decodeJSON(projectionBytes, &artifacts.Projection); err != nil {
		return Artifacts{}, false
	}
	if err := decodeJSON(proofBytes, &artifacts.Proof); err != nil {
		return Artifacts{}, false
	}
	if err := artifacts.Record.validate(); err != nil {
		return Artifacts{}, false
	}
	expectedProjectionBytes, err := canonicalJSON(Project(artifacts.Record))
	if err != nil || !bytes.Equal(projectionBytes, expectedProjectionBytes) {
		return Artifacts{}, false
	}
	proof := artifacts.Proof
	if proof.ExtractorVersion != ExtractorVersion || proof.OriginalSHA256 != originalSHA256 ||
		artifacts.Record.OriginalSHA256 != originalSHA256 || artifacts.Projection.OriginalSHA256 != originalSHA256 ||
		artifacts.Projection.ExtractorVersion != ExtractorVersion ||
		proof.RecordSHA256 != bytesSHA256(recordBytes) || proof.ProjectionSHA256 != bytesSHA256(projectionBytes) ||
		proof.FieldCount != countFields(artifacts.Record) || proof.RenderedCount != artifacts.Projection.RenderedFieldCount ||
		proof.ExclusionCount != len(artifacts.Projection.Exclusions) || proof.FieldCount != proof.RenderedCount+proof.ExclusionCount {
		return Artifacts{}, false
	}
	return artifacts, true
}

// ReadCheckedArtifacts opens an already checked metadata record without
// creating a cache directory or invoking ImageIO. It is for read-only audit
// paths that must not turn missing metadata into a new extraction.
func ReadCheckedArtifacts(root, originalSHA256 string) (Artifacts, bool) {

	originalSHA256 = strings.ToLower(strings.TrimSpace(originalSHA256))
	if validateSHA256(originalSHA256) != nil {
		return Artifacts{}, false
	}
	return readCheckedArtifacts(filepath.Join(strings.TrimSpace(root), originalSHA256, ExtractorVersion), originalSHA256)
}

func privateCacheDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode().Perm() == 0o700
}

func privateRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm() == 0o600
}

func canonicalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains a second value")
		}
		return err
	}
	return nil
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func writeAtomic(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".metadata-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			if _, err := hash.Write(buffer[:n]); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func bytesSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
