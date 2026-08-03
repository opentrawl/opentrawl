package archive

import "strings"

const knownPlaceObservationType = "known_place"

func KnownPlaceCardLine(kind, name string, after bool) string {
	name = strings.TrimSpace(name)
	if after {
		label := "At former home"
		if kind == "work" {
			label = "At former workplace"
		}
		if name == "" {
			return label
		}
		return label + " (" + name + ")"
	}
	switch kind {
	case "home":
		return "At home"
	case "former_home":
		if name == "" {
			return "At home at the time"
		}
		return "At home at the time (" + name + ")"
	case "work":
		if name == "" {
			return "At work"
		}
		return "At work (" + name + ")"
	default:
		return ""
	}
}
