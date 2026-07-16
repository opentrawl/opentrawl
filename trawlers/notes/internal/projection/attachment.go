package projection

import "strings"

const utiTable = "com.apple.notes.table"

// tableNotCaptured is emitted for a table whose companion CRDT blob was never
// archived (every table synced before table capture). It is an honest, temporary
// data gap, not a decode failure.
const tableNotCaptured = "[table: not yet captured, run trawl sync notes to fill in]"

// imageUTIs render as a bare "[image]" marker.
var imageUTIs = map[string]bool{
	"public.jpeg":          true,
	"public.png":           true,
	"public.heic":          true,
	"public.tiff":          true,
	"org.webmproject.webp": true,
}

// attachmentLabels maps a type UTI to the word inside "[attachment: word]".
// Unknown UTIs fall back to the UTI's last dot-segment.
var attachmentLabels = map[string]string{
	"com.apple.notes.gallery":                              "gallery",
	"com.apple.notes.inlinetextattachment.calculateresult": "calculation",
	"com.apple.notes.inlinetextattachment.mention":         "mention",
	"com.apple.paper.doc.scan":                             "scan",
	"com.apple.m4a-audio":                                  "audio",
	"com.adobe.pdf":                                        "pdf",
	"public.url":                                           "link",
	"com.apple.ical.ics":                                   "calendar",
	"org.openxmlformats.wordprocessingml.document":         "document",
	"public.plain-text":                                    "document",
}

// markerFor renders the embed marker for an attachment run. Tables resolve to
// a real pipe table when their companion blob is available; everything else is
// a fixed "[image]" or "[attachment: label]" marker. The note's own zdata does
// not carry a human filename (that lives in a separate SQLite metadata row), so
// the label is derived from the type UTI, not a filename.
func markerFor(info attachmentInfo, resolve TableResolver) string {
	if info.typeUTI == utiTable {
		return tableMarker(info.identifier, resolve)
	}
	if imageUTIs[info.typeUTI] {
		return "[image]"
	}
	return "[attachment: " + labelForUTI(info.typeUTI) + "]"
}

func labelForUTI(uti string) string {
	if label, ok := attachmentLabels[uti]; ok {
		return label
	}
	if uti == "" {
		return "unknown"
	}
	if idx := strings.LastIndex(uti, "."); idx >= 0 && idx < len(uti)-1 {
		return uti[idx+1:]
	}
	return uti
}
