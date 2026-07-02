package harness

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	hexSecretMinLength        = 40
	base64SecretMinLength     = 44
	secretFieldMinLength      = 16
	secretFieldShortMinLength = 6
)

var (
	hexSecretPattern = regexp.MustCompile(fmt.Sprintf(`\b[0-9a-fA-F]{%d,}\b`, hexSecretMinLength))
	// The slash is deliberately absent from the class: crawlers
	// legitimately print long file paths, and including "/" turns every
	// path into one giant match. JWTs get their own pattern below.
	base64SecretPattern = regexp.MustCompile(fmt.Sprintf(`\b[A-Za-z0-9+]{%d,}={0,2}\b`, base64SecretMinLength))
	jwtSecretPattern    = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{14,}\.[A-Za-z0-9_.-]+\b`)
	secretNameFragments = []string{"token", "cookie", "secret", "key"}
	secretScanCommands  = [][]string{
		{"metadata", "--json"},
		{"status", "--json"},
		{"doctor", "--json"},
	}
)

type secretHit struct {
	command string
	detail  string
}

func (s Suite) CheckSecrets(ctx context.Context) CheckResult {
	incomplete := false
	for _, args := range secretScanCommands {
		out := s.Runner.Run(ctx, args...)
		command := strings.Join(args, " ")
		if hit, ok := scanRawSecrets(command, out.Stderr); ok {
			return fail(CheckSecrets, hit.detail, "remove tokens, cookies, secrets and keys from command output")
		}
		value, err := decodeJSONValue(out.Stdout)
		if err != nil || !out.OK() {
			// Not JSON, so no key context: pattern-scan the raw bytes.
			if hit, ok := scanRawSecrets(command, out.Stdout); ok {
				return fail(CheckSecrets, hit.detail, "remove tokens, cookies, secrets and keys from command output")
			}
			incomplete = true
			continue
		}
		if hit, ok := scanJSONSecrets(command, value); ok {
			return fail(CheckSecrets, hit.detail, "emit auth as booleans and expiry only; remove secret string fields")
		}
	}
	if incomplete {
		return warn(CheckSecrets, "secret scan ran, but one JSON command did not complete cleanly")
	}
	return pass(CheckSecrets, "no secret-like strings found")
}

func scanRawSecrets(command string, data []byte) (secretHit, bool) {
	text := string(data)
	if hexSecretPattern.FindString(text) != "" {
		return secretHit{command: command, detail: "long hex-like string in " + command + " output"}, true
	}
	for _, match := range base64SecretPattern.FindAllString(text, -1) {
		if looksLikeBase64Run(match) {
			return secretHit{command: command, detail: "long base64-like string in " + command + " output"}, true
		}
	}
	if jwtSecretPattern.FindString(text) != "" {
		return secretHit{command: command, detail: "JWT-like string in " + command + " output"}, true
	}
	return secretHit{}, false
}

func scanJSONSecrets(command string, value any) (secretHit, bool) {
	return scanJSONSecretsAt(command, "", value)
}

func scanJSONSecretsAt(command, path string, value any) (secretHit, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := joinJSONPath(path, key)
			if secretFieldName(key) {
				if text, ok := child.(string); ok && looksLikeSecretFieldValue(text) {
					return secretHit{command: command, detail: "secret-like field " + childPath + " in " + command + " output"}, true
				}
			}
			if hit, ok := scanJSONSecretsAt(command, childPath, child); ok {
				return hit, true
			}
		}
	case []any:
		for _, child := range typed {
			if hit, ok := scanJSONSecretsAt(command, path, child); ok {
				return hit, true
			}
		}
	case string:
		// File paths legitimately contain long random-looking runs, so
		// path-like keys and path-shaped values are exempt from the raw
		// patterns; the key-name scan above still covers them.
		if pathLikeKey(lastJSONKey(path)) || strings.ContainsAny(typed, `/\`) {
			return secretHit{}, false
		}
		if hit, ok := scanRawSecrets(command, []byte(typed)); ok {
			return secretHit{command: command, detail: hit.detail + " (field " + path + ")"}, true
		}
	}
	return secretHit{}, false
}

var pathLikeKeyFragments = []string{"path", "file", "dir", "location", "endpoint", "archive", "database", "url"}

func pathLikeKey(name string) bool {
	lower := strings.ToLower(name)
	for _, fragment := range pathLikeKeyFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func lastJSONKey(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

func secretFieldName(name string) bool {
	lower := strings.ToLower(name)
	for _, fragment := range secretNameFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func looksLikeSecretFieldValue(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < secretFieldShortMinLength {
		return false
	}
	if len(value) >= secretFieldMinLength {
		return true
	}
	return containsDigit(value) || containsSymbol(value) || hasMixedCase(value)
}

func looksLikeBase64Run(value string) bool {
	value = strings.TrimRight(value, "=")
	return strings.ContainsAny(value, "+/") || categories(value) >= 3
}

func containsDigit(value string) bool {
	for _, r := range value {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func containsSymbol(value string) bool {
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func hasMixedCase(value string) bool {
	hasLower := false
	hasUpper := false
	for _, r := range value {
		hasLower = hasLower || unicode.IsLower(r)
		hasUpper = hasUpper || unicode.IsUpper(r)
	}
	return hasLower && hasUpper
}

func categories(value string) int {
	seen := map[string]bool{}
	for _, r := range value {
		switch {
		case unicode.IsLower(r):
			seen["lower"] = true
		case unicode.IsUpper(r):
			seen["upper"] = true
		case unicode.IsDigit(r):
			seen["digit"] = true
		default:
			seen["symbol"] = true
		}
	}
	return len(seen)
}

func joinJSONPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
