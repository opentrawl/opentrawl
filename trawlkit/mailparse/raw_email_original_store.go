package mailparse

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

// RawEmailOriginalSHA256 is the lower-case hex SHA-256 of the raw RFC 5322
// bytes, before compression.
type RawEmailOriginalSHA256 string

// RawEmailOriginalStore keeps each raw RFC 5322 message once, compressed with
// zstd, at originals/<first two hex digits>/<sha256>.eml.zst. A stored file
// is never rewritten, so a repeated update writes nothing.
type RawEmailOriginalStore struct {
	rawEmailOriginalsDirectoryPath string
}

var (
	rawEmailOriginalEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	rawEmailOriginalDecoder, _ = zstd.NewReader(nil)
)

func NewRawEmailOriginalStore(trawlerArchiveDirectoryPath string) RawEmailOriginalStore {
	return RawEmailOriginalStore{rawEmailOriginalsDirectoryPath: filepath.Join(trawlerArchiveDirectoryPath, "originals")}
}

func (store RawEmailOriginalStore) StoreRawEmailOriginalOnce(rawEmailMessage []byte) (RawEmailOriginalSHA256, error) {
	digest := sha256.Sum256(rawEmailMessage)
	originalSHA256 := RawEmailOriginalSHA256(hex.EncodeToString(digest[:]))
	targetPath := store.rawEmailOriginalPath(originalSHA256)
	if _, err := os.Stat(targetPath); err == nil {
		return originalSHA256, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect raw email original: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return "", fmt.Errorf("create raw email original directory: %w", err)
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(targetPath), "."+string(originalSHA256)+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create raw email original: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	_, writeErr := temporaryFile.Write(rawEmailOriginalEncoder.EncodeAll(rawEmailMessage, nil))
	var syncErr error
	if writeErr == nil {
		syncErr = temporaryFile.Sync()
	}
	closeErr := temporaryFile.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("write raw email original: %w", err)
	}
	if err := os.Rename(temporaryPath, targetPath); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("store raw email original: %w", err)
	}
	return originalSHA256, nil
}

func (store RawEmailOriginalStore) ReadRawEmailOriginal(originalSHA256 RawEmailOriginalSHA256) ([]byte, error) {
	compressed, err := os.ReadFile(store.rawEmailOriginalPath(originalSHA256))
	if err != nil {
		return nil, fmt.Errorf("read raw email original: %w", err)
	}
	rawEmailMessage, err := rawEmailOriginalDecoder.DecodeAll(compressed, nil)
	if err != nil {
		return nil, fmt.Errorf("decompress raw email original: %w", err)
	}
	if digest := sha256.Sum256(rawEmailMessage); hex.EncodeToString(digest[:]) != string(originalSHA256) {
		return nil, fmt.Errorf("raw email original %s does not match its SHA-256", originalSHA256)
	}
	return rawEmailMessage, nil
}

func (store RawEmailOriginalStore) rawEmailOriginalPath(originalSHA256 RawEmailOriginalSHA256) string {
	return filepath.Join(store.rawEmailOriginalsDirectoryPath, string(originalSHA256[:2]), string(originalSHA256)+".eml.zst")
}
