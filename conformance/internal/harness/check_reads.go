package harness

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var databaseFileExtensions = []string{".db", ".sqlite", ".sqlite3"}

var readProbeCommands = [][]string{
	{"status", "--json"},
	{"doctor", "--json"},
	{"metadata", "--json"},
}

type pathCandidate struct {
	path  string
	score int
}

func (s Suite) CheckReadsNeverMutate(ctx context.Context, status StatusInfo) CheckResult {
	statusValue := status.Value
	if !status.Valid {
		out := s.Runner.Run(ctx, "status", "--json")
		if !out.OK() {
			return warn(CheckReadsNeverMutate, "archive database path was not checked because status --json failed")
		}
		decoded, err := decodeJSONObject(out.Stdout)
		if err != nil {
			return warn(CheckReadsNeverMutate, "archive database path was not checked because status JSON did not parse")
		}
		statusValue = decoded
	}
	archivePath, ok := findArchiveDatabasePath(statusValue)
	if !ok {
		return warn(CheckReadsNeverMutate, "status JSON did not expose an absolute archive database path")
	}
	before, err := fileSHA256(archivePath)
	if err != nil {
		return warn(CheckReadsNeverMutate, "archive database path was found but could not be read")
	}
	probeFailures := []string{}
	for _, args := range readProbeCommands {
		out := s.Runner.Run(ctx, args...)
		if !out.OK() {
			probeFailures = append(probeFailures, strings.Join(args, " "))
		}
	}
	after, err := fileSHA256(archivePath)
	if err != nil {
		return fail(CheckReadsNeverMutate, "archive database became unreadable after read commands", "make metadata, status and doctor read-only")
	}
	if before != after {
		return fail(CheckReadsNeverMutate, "archive database hash changed after read commands", "remove writes from metadata, status and doctor")
	}
	if len(probeFailures) > 0 {
		return warn(CheckReadsNeverMutate, "archive hash stayed stable, but a read command did not complete")
	}
	return pass(CheckReadsNeverMutate, "archive hash stayed stable after read commands")
}

func findArchiveDatabasePath(status map[string]any) (string, bool) {
	candidates := collectPathCandidates(status, nil, nil)
	if len(candidates) == 0 {
		return "", false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})
	return candidates[0].path, true
}

func collectPathCandidates(value any, ancestors []string, owner map[string]any) []pathCandidate {
	switch typed := value.(type) {
	case map[string]any:
		out := []pathCandidate{}
		for key, child := range typed {
			out = append(out, collectPathCandidatesForKey(key, child, ancestors, typed)...)
		}
		return out
	case []any:
		out := []pathCandidate{}
		for _, child := range typed {
			out = append(out, collectPathCandidates(child, ancestors, owner)...)
		}
		return out
	default:
		return nil
	}
}

func collectPathCandidatesForKey(key string, value any, ancestors []string, owner map[string]any) []pathCandidate {
	nextAncestors := append(append([]string(nil), ancestors...), key)
	text, ok := value.(string)
	if ok && databasePathLooksUsable(text) {
		return []pathCandidate{{
			path:  text,
			score: databasePathScore(key, nextAncestors, owner),
		}}
	}
	return collectPathCandidates(value, nextAncestors, owner)
}

func databasePathLooksUsable(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || !filepath.IsAbs(value) {
		return false
	}
	ext := strings.ToLower(filepath.Ext(value))
	for _, allowed := range databaseFileExtensions {
		if ext == allowed {
			return true
		}
	}
	return false
}

func databasePathScore(key string, ancestors []string, owner map[string]any) int {
	score := 20
	switch strings.ToLower(key) {
	case "database_path", "db_path", "archive_path":
		score += 100
	case "path":
		score += 70
	case "database", "db":
		score += 60
	}
	for _, ancestor := range ancestors {
		lower := strings.ToLower(ancestor)
		if strings.Contains(lower, "archive") {
			score += 60
		}
		if strings.Contains(lower, "database") {
			score += 20
		}
		if strings.Contains(lower, "source") {
			score -= 40
		}
	}
	if stringValue(owner, "role") == "archive" {
		score += 70
	}
	if stringValue(owner, "kind") == "sqlite" {
		score += 10
	}
	if primary, ok := owner["is_primary"].(bool); ok && primary {
		score += 20
	}
	return score
}

func stringValue(object map[string]any, key string) string {
	value, ok := object[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(value))
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- path comes from the crawler status contract and is only read.
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var out [sha256.Size]byte
	copy(out[:], hash.Sum(nil))
	return out, nil
}
