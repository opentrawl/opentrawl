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
