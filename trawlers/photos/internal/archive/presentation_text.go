package archive

import "strings"

func truncateReason(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= 200 {
		return value
	}
	return strings.TrimSpace(value[:200])
}

func cleanPlacePhrase(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return ""
	}
	if sentence, _, ok := strings.Cut(value, "."); ok {
		value = strings.TrimSpace(sentence)
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{
		"the image was taken in an ",
		"the image was taken in a ",
		"the image was taken in ",
		"this image was taken in an ",
		"this image was taken in a ",
		"this image was taken in ",
		"the photo was taken in an ",
		"the photo was taken in a ",
		"the photo was taken in ",
		"this appears to be an ",
		"this appears to be a ",
		"it appears to be an ",
		"it appears to be a ",
	} {
		if strings.HasPrefix(lower, prefix) {
			value = strings.TrimSpace(value[len(prefix):])
			break
		}
	}
	if len(value) <= 90 {
		return value
	}
	cut := strings.LastIndexAny(value[:90], ",;")
	if cut < 30 {
		cut = strings.LastIndex(value[:90], " ")
	}
	if cut < 30 {
		return strings.TrimSpace(value[:90])
	}
	return strings.TrimSpace(value[:cut])
}
