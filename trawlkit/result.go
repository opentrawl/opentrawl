package trawlkit

import (
	"io"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func writeResult(w io.Writer, format output.Format, label string, value any) error {
	if format != output.Text {
		value = normalizeJSONResult(value)
		return output.Write(w, format, label, value)
	}
	switch v := value.(type) {
	case control.Manifest:
		return writeManifestText(w, v)
	case *control.Status:
		return render.WriteStatus(w, renderStatus(v))
	case *SyncReport:
		return writeSyncReportText(w, v)
	case searchOutput:
		return writeSearchText(w, v)
	case whoOutput:
		return writeWhoText(w, v)
	case chatsOutput:
		return writeChatsText(w, v)
	case *control.ContactExport:
		return writeContactsText(w, v)
	default:
		return output.Write(w, format, label, value)
	}
}

func normalizeJSONResult(value any) any {
	switch v := value.(type) {
	case searchOutput:
		if v.Results == nil {
			v.Results = []Hit{}
		}
		return v
	case *control.ContactExport:
		if v == nil {
			return &control.ContactExport{Contacts: []control.Contact{}}
		}
		out := *v
		if out.Contacts == nil {
			out.Contacts = []control.Contact{}
		}
		return &out
	case whoOutput:
		if v.Candidates == nil {
			v.Candidates = []whoCandidateOutput{}
		}
		return v
	case chatsOutput:
		if v.Chats == nil {
			v.Chats = []chatOutput{}
		}
		return v
	default:
		return value
	}
}
