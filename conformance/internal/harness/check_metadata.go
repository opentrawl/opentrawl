package harness

import (
	"context"
	"fmt"
	"strings"
)

func (s Suite) CheckMetadata(ctx context.Context) (CheckResult, MetadataInfo) {
	out := s.Runner.Run(ctx, "metadata", "--json")
	if !out.OK() {
		return fail(CheckMetadata, out.FailureDetail(), "make metadata --json return the crawler manifest"), MetadataInfo{}
	}
	manifest, err := decodeJSONObject(out.Stdout)
	if err != nil {
		return fail(CheckMetadata, "metadata --json did not return valid JSON", "emit one JSON object from metadata --json"), MetadataInfo{}
	}
	appID, ok := stringField(manifest, "id")
	if !ok {
		return fail(CheckMetadata, "metadata id is missing or empty", "set a stable non-empty id in metadata"), MetadataInfo{}
	}
	capabilitiesValue, ok := manifest["capabilities"]
	if !ok {
		return fail(CheckMetadata, "metadata capabilities is missing", "emit capabilities as a JSON array"), MetadataInfo{}
	}
	capabilities, ok := capabilitiesValue.([]any)
	if !ok {
		return fail(CheckMetadata, "metadata capabilities is not an array", "emit capabilities as a JSON array"), MetadataInfo{}
	}
	info := MetadataInfo{Capabilities: capabilityStrings(capabilities), AppID: appID, Valid: true}
	if !fieldPresent(manifest, "contract_version") && !fieldPresent(manifest, "schema_version") {
		return warn(CheckMetadata, "metadata has no contract_version or schema_version"), info
	}
	if schemaVersion(manifest) >= 2 {
		if hasCapability(info.Capabilities, "verbose_logs") {
			return fail(CheckMetadata, "schema v2 metadata still declares verbose_logs", "remove verbose_logs; runner-owned log streaming is not a manifest capability"), info
		}
		if failure := checkCommandFlagTables(manifest); failure != "" {
			return fail(CheckMetadata, failure, "emit a commands object where every command keeps argv/json/mutates fields and each flag has a unique non-empty name"), info
		}
	}
	return pass(CheckMetadata, fmt.Sprintf("manifest declares %d capabilities", len(capabilities))), info
}

func capabilityStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func schemaVersion(manifest map[string]any) int {
	value, ok := manifest["schema_version"]
	if !ok {
		return 0
	}
	number, ok := jsonNumber(value)
	if !ok {
		return 0
	}
	return int(number)
}

func checkCommandFlagTables(manifest map[string]any) string {
	rawCommands, ok := manifest["commands"].(map[string]any)
	if !ok || len(rawCommands) == 0 {
		return "schema v2 metadata is missing commands object"
	}
	for name, raw := range rawCommands {
		command, ok := raw.(map[string]any)
		if !ok {
			return fmt.Sprintf("command %q is not an object", name)
		}
		if failure := checkCommandFields(name, command); failure != "" {
			return failure
		}
		if flags, ok := command["flags"]; ok && flags != nil {
			flagList, ok := flags.([]any)
			if !ok {
				return fmt.Sprintf("command %q flags is not an array", name)
			}
			if failure := checkFlagList(name, flagList); failure != "" {
				return failure
			}
		} else if name == "search" {
			return `command "search" flags is missing`
		}
	}
	return ""
}

func checkCommandFields(name string, command map[string]any) string {
	argv, ok := command["argv"].([]any)
	if !ok {
		return fmt.Sprintf("command %q argv is missing or not an array", name)
	}
	if len(argv) == 0 {
		return fmt.Sprintf("command %q argv is empty", name)
	}
	for i, raw := range argv {
		arg, ok := raw.(string)
		if !ok || strings.TrimSpace(arg) == "" {
			return fmt.Sprintf("command %q argv %d is missing or empty", name, i)
		}
	}
	if _, ok := command["json"].(bool); !ok {
		return fmt.Sprintf("command %q json is missing or not a boolean", name)
	}
	if mutates, ok := command["mutates"]; !ok {
		return fmt.Sprintf("command %q mutates is missing", name)
	} else if _, ok := mutates.(bool); !ok {
		return fmt.Sprintf("command %q mutates is not a boolean", name)
	}
	return ""
}

func checkFlagList(command string, flags []any) string {
	seen := map[string]struct{}{}
	for i, raw := range flags {
		flagObject, ok := raw.(map[string]any)
		if !ok {
			return fmt.Sprintf("command %q flag %d is not an object", command, i)
		}
		name, ok := stringField(flagObject, "name")
		if !ok {
			return fmt.Sprintf("command %q flag %d has no name", command, i)
		}
		if _, ok := seen[name]; ok {
			return fmt.Sprintf("command %q repeats flag %q", command, name)
		}
		seen[name] = struct{}{}
	}
	return ""
}
