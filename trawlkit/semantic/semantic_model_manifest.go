package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	DefaultSemanticEmbeddingModelIdentifier = "mlx-community/Qwen3-Embedding-0.6B-4bit-DWQ"
	DefaultSemanticEmbeddingModelSlug       = "qwen3-embedding-0.6b-4bit-dwq"
	DefaultSemanticEmbeddingDimensions      = 512
	DefaultSemanticMaximumInputTokenCount   = 256
	DefaultSemanticChunkerSchemaName        = "session-2h"
	pinnedModelRevisionSHA                  = "6c3ae70858513f1a78e9cdca3cae330d9075cd2a"
	modelDownloadBaseURL                    = "https://huggingface.co/mlx-community/Qwen3-Embedding-0.6B-4bit-DWQ/resolve/" + pinnedModelRevisionSHA + "/"
)

// SemanticModelArtifactSpec defines one pinned, SHA-256 verified file required
// by the local Apple Silicon MLX embedding model.
type SemanticModelArtifactSpec struct {
	Filename  string
	ByteCount int64
	SHA256Hex string
}

var defaultQwen3EmbeddingArtifacts = []SemanticModelArtifactSpec{
	{Filename: "config.json", ByteCount: 937, SHA256Hex: "e7dfa5b73fb2a03cbc8fb40c394e95b99f03348e237f7f28e7a1daf56a2169bb"},
	{Filename: "generation_config.json", ByteCount: 117, SHA256Hex: "28396d421a2108acce96383f6a7de78008f7f1b17f807958f3c14c51dbfb65fb"},
	{Filename: "model.safetensors", ByteCount: 335296756, SHA256Hex: "3d773d5ee582eda445daeee23f7a2b76124011796df244ddb45e22638fdb7cde"},
	{Filename: "model.safetensors.index.json", ByteCount: 49770, SHA256Hex: "90d82744cdb6b7d093f0b812fc21a49b6ffa9d0084a45428f0cfd01eb4adbe12"},
	{Filename: "added_tokens.json", ByteCount: 707, SHA256Hex: "c0284b582e14987fbd3d5a2cb2bd139084371ed9acbae488829a1c900833c680"},
	{Filename: "special_tokens_map.json", ByteCount: 613, SHA256Hex: "76862e765266b85aa9459767e33cbaf13970f327a0e88d1c65846c2ddd3a1ecd"},
	{Filename: "tokenizer.json", ByteCount: 11423705, SHA256Hex: "def76fb086971c7867b829c23a26261e38d9d74e02139253b38aeb9df8b4b50a"},
	{Filename: "tokenizer_config.json", ByteCount: 5404, SHA256Hex: "443bfa629eb16387a12edbf92a76f6a6f10b2af3b53d87ba1550adfcf45f7fa0"},
	{Filename: "vocab.json", ByteCount: 2776833, SHA256Hex: "ca10d7e9fb3ed18575dd1e277a2579c16d108e32f27439684afa0e10b1440910"},
	{Filename: "merges.txt", ByteCount: 1671853, SHA256Hex: "8831e4f1a044471340f7c0a83d7bd71306a5b867e95fd870f74d0c5308a904d5"},
}

// ResolveDefaultModelCacheDirectory returns ~/Library/Caches/OpenTrawl/models/<slug>
// (or OPENTRAWL_SEMANTIC_MODEL_DIR when overridden).
func ResolveDefaultModelCacheDirectory() (string, error) {
	if override := strings.TrimSpace(os.Getenv("OPENTRAWL_SEMANTIC_MODEL_DIR")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for semantic model cache: %w", err)
	}
	return filepath.Join(home, "Library", "Caches", "OpenTrawl", "models", DefaultSemanticEmbeddingModelSlug), nil
}

// CheckDefaultModelCachedAndVerified reports whether every pinned file is
// present on disk with the expected size (and optional full SHA-256 verification).
func CheckDefaultModelCachedAndVerified(modelDir string, verifyHashes bool) (bool, uint64, error) {
	var totalBytes uint64
	for _, spec := range defaultQwen3EmbeddingArtifacts {
		path := filepath.Join(modelDir, spec.Filename)
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, 0, nil
			}
			return false, 0, err
		}
		if info.Size() != spec.ByteCount {
			return false, 0, nil
		}
		if verifyHashes {
			digest, hashErr := computeFileSHA256Hex(path)
			if hashErr != nil {
				return false, 0, hashErr
			}
			if digest != spec.SHA256Hex {
				return false, 0, nil
			}
		}
		totalBytes += uint64(info.Size())
	}
	return true, totalBytes, nil
}

// EnsureDefaultModelDownloaded downloads any missing or corrupted model files
// into modelDir and verifies every file against its pinned SHA-256 digest.
func EnsureDefaultModelDownloaded(ctx context.Context, modelDir string) (uint64, error) {
	if err := os.MkdirAll(modelDir, 0o700); err != nil {
		return 0, fmt.Errorf("create semantic model cache directory: %w", err)
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	var totalBytes uint64
	for _, spec := range defaultQwen3EmbeddingArtifacts {
		destPath := filepath.Join(modelDir, spec.Filename)
		if ok, err := fileMatchesArtifactSpec(destPath, spec); err == nil && ok {
			totalBytes += uint64(spec.ByteCount)
			continue
		}
		if err := downloadAndVerifyArtifact(ctx, client, destPath, spec); err != nil {
			return 0, err
		}
		totalBytes += uint64(spec.ByteCount)
	}
	return totalBytes, nil
}

func fileMatchesArtifactSpec(path string, spec SemanticModelArtifactSpec) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.Size() != spec.ByteCount {
		return false, nil
	}
	digest, err := computeFileSHA256Hex(path)
	if err != nil {
		return false, err
	}
	return digest == spec.SHA256Hex, nil
}

func downloadAndVerifyArtifact(
	ctx context.Context,
	client *http.Client,
	destPath string,
	spec SemanticModelArtifactSpec,
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelDownloadBaseURL+spec.Filename, nil)
	if err != nil {
		return fmt.Errorf("prepare download request for %s: %w", spec.Filename, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", spec.Filename, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected HTTP status %d", spec.Filename, resp.StatusCode)
	}

	tmpPath := destPath + ".download"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", spec.Filename, err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write %s: %w", spec.Filename, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close %s: %w", spec.Filename, closeErr)
	}
	if written != spec.ByteCount {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("verify %s size: got %d bytes, expected %d", spec.Filename, written, spec.ByteCount)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if digest != spec.SHA256Hex {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("verify %s SHA-256: got %s, expected %s", spec.Filename, digest, spec.SHA256Hex)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("install verified %s: %w", spec.Filename, err)
	}
	return nil
}

func computeFileSHA256Hex(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- path is derived from fixed manifest filenames inside the local cache directory.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
