package crawlkit

import (
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/config"
	"github.com/openclaw/crawlkit/output"
)

type archiveFilenameDeclarationError struct {
	filename string
}

func (e archiveFilenameDeclarationError) Error() string {
	if strings.TrimSpace(e.filename) == "" {
		return "invalid archive filename: archive filename is empty"
	}
	return fmt.Sprintf("invalid archive filename %q: archive filename must be a filename, not a path", strings.TrimSpace(e.filename))
}

func (e archiveFilenameDeclarationError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{
		Code:    "invalid_archive_filename",
		Message: e.Error(),
		Remedy:  "Set ArchiveFilename to one filename only; remove directories, path separators, and \"..\".",
	}
}

func archiveFilename(info Info) (string, error) {
	filename, err := config.ArchiveFilename(info.ID, info.ArchiveFilename)
	if err != nil {
		return "", archiveFilenameDeclarationError{filename: info.ArchiveFilename}
	}
	return filename, nil
}

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

func verbDisplayName(verb Verb) string {
	name := strings.Join(strings.Fields(verb.Name), " ")
	if name == "" {
		return "unnamed"
	}
	return name
}
