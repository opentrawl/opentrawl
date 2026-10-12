package applelibrary

import (
	"errors"
	"fmt"

	"howett.net/plist"
)

// appleKeyedArchive is an NSKeyedArchiver property list: objects refer to each
// other by index into $objects.
type appleKeyedArchive struct {
	archivedObjects []any
	rootObject      map[string]any
}

const appleKeyedArchiveNullObject = "$null"

func decodeAppleKeyedArchive(archiveData []byte) (appleKeyedArchive, error) {
	var archivePropertyList map[string]any
	if _, err := plist.Unmarshal(archiveData, &archivePropertyList); err != nil {
		return appleKeyedArchive{}, fmt.Errorf("read keyed archive: %w", err)
	}
	archivedObjects, _ := archivePropertyList["$objects"].([]any)
	topObjects, _ := archivePropertyList["$top"].(map[string]any)
	keyedArchive := appleKeyedArchive{archivedObjects: archivedObjects}
	rootObject, ok := keyedArchive.resolve(topObjects["root"]).(map[string]any)
	if !ok {
		return appleKeyedArchive{}, errors.New("keyed archive has no root object")
	}
	keyedArchive.rootObject = rootObject
	return keyedArchive, nil
}

func (keyedArchive appleKeyedArchive) resolve(value any) any {
	if reference, ok := value.(plist.UID); ok {
		if int(reference) >= len(keyedArchive.archivedObjects) {
			return nil
		}
		value = keyedArchive.archivedObjects[reference]
	}
	if value == appleKeyedArchiveNullObject {
		return nil
	}
	return value
}

func (keyedArchive appleKeyedArchive) text(value any) string {
	text, _ := keyedArchive.resolve(value).(string)
	return text
}

func (keyedArchive appleKeyedArchive) dictionary(value any) map[string]any {
	dictionary, _ := keyedArchive.resolve(value).(map[string]any)
	return dictionary
}

func (keyedArchive appleKeyedArchive) array(value any) []any {
	array, _ := keyedArchive.dictionary(value)["NS.objects"].([]any)
	return array
}

func (keyedArchive appleKeyedArchive) number(value any) (float64, bool) {
	switch number := keyedArchive.resolve(value).(type) {
	case float64:
		return number, true
	case int64:
		return float64(number), true
	case uint64:
		return float64(number), true
	default:
		return 0, false
	}
}
