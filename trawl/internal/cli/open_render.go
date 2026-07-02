package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const jsonIndent = "  "

func renderOpenPayload(w io.Writer, value any) error {
	return renderJSONValue(w, value, 0)
}

func renderJSONValue(w io.Writer, value any, indent int) error {
	prefix := strings.Repeat(jsonIndent, indent)
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if isJSONScalar(typed[key]) {
				if _, err := fmt.Fprintf(w, "%s%s: %s\n", prefix, key, jsonScalarText(typed[key])); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(w, "%s%s:\n", prefix, key); err != nil {
				return err
			}
			if err := renderJSONValue(w, typed[key], indent+1); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if isJSONScalar(item) {
				if _, err := fmt.Fprintf(w, "%s- %s\n", prefix, jsonScalarText(item)); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(w, "%s-\n", prefix); err != nil {
				return err
			}
			if err := renderJSONValue(w, item, indent+1); err != nil {
				return err
			}
		}
	default:
		_, err := fmt.Fprintf(w, "%s%s\n", prefix, jsonScalarText(typed))
		return err
	}
	return nil
}

func isJSONScalar(value any) bool {
	switch value.(type) {
	case nil, string, bool, json.Number, float64:
		return true
	default:
		return false
	}
}

func jsonScalarText(value any) string {
	switch typed := value.(type) {
	case nil:
		return unknownFreshness
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprint(typed)
	default:
		return fmt.Sprint(typed)
	}
}
