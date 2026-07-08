package crawlkit

import (
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/output"
)

func supportedVerbDeclarations(source Crawler) (map[string]Verb, error) {
	spine, err := supportedSpineVerbDeclarations(source)
	if err != nil {
		return nil, err
	}
	if err := validateBespokeVerbs(source); err != nil {
		return nil, err
	}
	return spine, nil
}

type verbDeclarationError struct {
	name    string
	message string
	remedy  string
}

func (e verbDeclarationError) Error() string {
	return fmt.Sprintf("invalid %s Verb declaration: %s", strings.TrimSpace(e.name), e.message)
}

func (e verbDeclarationError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{
		Code:    "invalid_verb_declaration",
		Message: e.Error(),
		Remedy:  e.remedy,
	}
}

func validateBespokeVerbs(source Crawler) error {
	for _, verb := range source.Verbs() {
		if _, ok := spineVerbKey(verb.Name); ok {
			continue
		}
		if _, err := storeModeForVerb(verb); err != nil {
			return err
		}
	}
	return nil
}

func storeModeForVerb(verb Verb) (storeMode, error) {
	switch verb.Store {
	case StoreDefault:
		if verb.Mutates {
			return storeWrite, nil
		}
		return storeRead, nil
	case StoreNone:
		return storeNone, nil
	case StoreOptional:
		if verb.Mutates {
			return 0, verbDeclarationError{
				name:    verbDisplayName(verb),
				message: "StoreOptional cannot be used with Mutates",
				remedy:  "Set Store to StoreNone for a mutating verb that does not use the archive, or StoreRequired for a mutating verb that writes the archive.",
			}
		}
		return storeOptional, nil
	case StoreRequired:
		if verb.Mutates {
			return storeWrite, nil
		}
		return storeRead, nil
	default:
		return 0, verbDeclarationError{
			name:    verbDisplayName(verb),
			message: fmt.Sprintf("Store has unknown value %d", verb.Store),
			remedy:  "Use StoreDefault, StoreNone, StoreOptional, or StoreRequired.",
		}
	}
}

func storeAccessName(access StoreAccess) string {
	switch access {
	case StoreDefault:
		return "StoreDefault"
	case StoreNone:
		return "StoreNone"
	case StoreOptional:
		return "StoreOptional"
	case StoreRequired:
		return "StoreRequired"
	default:
		return fmt.Sprintf("StoreAccess(%d)", access)
	}
}

func storeModeGoName(mode storeMode) string {
	switch mode {
	case storeNone:
		return "storeNone"
	case storeOptional:
		return "storeOptional"
	case storeRead:
		return "storeRead"
	case storeWrite:
		return "storeWrite"
	default:
		return fmt.Sprintf("storeMode(%d)", mode)
	}
}

func storeModeManifestValue(mode storeMode) string {
	switch mode {
	case storeNone:
		return "none"
	case storeOptional:
		return "optional"
	case storeRead:
		return "read"
	case storeWrite:
		return "write"
	default:
		return ""
	}
}

func verbDisplayName(verb Verb) string {
	name := strings.Join(strings.Fields(verb.Name), " ")
	if name == "" {
		return "unnamed"
	}
	return name
}
