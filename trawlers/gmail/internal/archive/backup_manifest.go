package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func LoadBackupManifest(repo string) ([]BackupShard, error) {
	data, err := os.ReadFile(filepath.Join(repo, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read backup manifest: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode backup manifest: %w", err)
	}
	seen := map[string]BackupShard{}
	collectManifestShards(raw, seen)
	out := make([]BackupShard, 0, len(seen))
	for _, shard := range seen {
		if shard.Hash == "" {
			hash, err := encryptedShardHash(repo, shard.Path)
			if err != nil {
				return nil, err
			}
			shard.Hash = hash
		}
		out = append(out, shard)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == BackupShardLabels
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func collectManifestShards(value any, seen map[string]BackupShard) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			collectManifestShards(item, seen)
		}
	case map[string]any:
		if shard, ok := shardFromManifestMap(typed); ok {
			seen[shard.Path] = mergeShard(seen[shard.Path], shard)
		}
		for key, item := range typed {
			if kind, ok := shardKind(key); ok {
				shard := BackupShard{Path: filepath.ToSlash(key), Kind: kind}
				if itemMap, ok := item.(map[string]any); ok {
					shard.Hash = manifestHash(itemMap)
					shard.Rows = manifestRows(itemMap)
				}
				seen[shard.Path] = mergeShard(seen[shard.Path], shard)
			}
			collectManifestShards(item, seen)
		}
	}
}

func shardFromManifestMap(raw map[string]any) (BackupShard, bool) {
	var shard BackupShard
	for key, value := range raw {
		if !isPathKey(key) {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		kind, ok := shardKind(text)
		if !ok {
			continue
		}
		shard.Path = filepath.ToSlash(text)
		shard.Kind = kind
		break
	}
	if shard.Path == "" {
		return BackupShard{}, false
	}
	shard.Hash = manifestHash(raw)
	shard.Rows = manifestRows(raw)
	return shard, true
}

func mergeShard(old, next BackupShard) BackupShard {
	if old.Path == "" {
		return next
	}
	if next.Hash != "" {
		old.Hash = next.Hash
	}
	if next.Rows != 0 {
		old.Rows = next.Rows
	}
	if next.Kind != "" {
		old.Kind = next.Kind
	}
	return old
}

func isPathKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "path", "shard", "file", "name":
		return true
	default:
		return false
	}
}

func shardKind(value string) (BackupShardKind, bool) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if !strings.HasSuffix(value, ".jsonl.gz.age") || !strings.Contains(value, "gmail") {
		return "", false
	}
	base := path.Base(value)
	switch {
	case base == "labels.jsonl.gz.age" || strings.Contains(base, "labels"):
		return BackupShardLabels, true
	case strings.Contains(value, "/messages/") || strings.HasPrefix(base, "part-"):
		return BackupShardMessages, true
	default:
		return "", false
	}
}

func manifestHash(raw map[string]any) string {
	preferred := []string{
		"plaintext_sha256",
		"plaintextSHA256",
		"plain_sha256",
		"sha256",
		"hash",
		"digest",
	}
	for _, key := range preferred {
		if value, ok := raw[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	for key, value := range raw {
		lower := strings.ToLower(key)
		if !strings.Contains(lower, "hash") && !strings.Contains(lower, "sha256") {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func manifestRows(raw map[string]any) int64 {
	for _, key := range []string{"rows", "row_count", "rowCount", "count"} {
		if rows, ok := manifestInt(raw[key]); ok {
			return rows
		}
	}
	return 0
}

func manifestInt(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		out, err := typed.Int64()
		return out, err == nil
	case float64:
		return int64(typed), true
	case string:
		out, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return out, err == nil
	default:
		return 0, false
	}
}

func encryptedShardHash(repo, shard string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(shard)))
	if err != nil {
		return "", fmt.Errorf("hash backup shard %s: %w", shard, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
