package crawlkit

import (
	"io"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
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
	case *Doctor:
		return render.WriteDoctor(w, renderDoctorChecks(v), render.LogTail{})
	case *SyncReport:
		return writeSyncReportText(w, v)
	case searchOutput:
		return writeSearchText(w, v)
	case whoOutput:
		return writeWhoText(w, v)
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
	case *Doctor:
		if v == nil {
			return &Doctor{Checks: []Check{}}
		}
		out := *v
		if out.Checks == nil {
			out.Checks = []Check{}
		}
		return &out
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
	default:
		return value
	}
}
