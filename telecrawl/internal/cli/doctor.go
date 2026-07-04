package cli

import (
	"flag"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

func (r *runtime) runDoctor(args []string) error {
	fs := flag.NewFlagSet("telecrawl doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", r.source, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	report := telegramdesktop.Probe(r.ctx, telegramdesktop.Options{Path: *path})
	return r.print(r.doctorEnvelope(report))
}

func (r *runtime) printDoctor(value doctorOutput) error {
	return render.WriteDoctor(r.stdout, doctorRenderChecks(value.Checks), value.logTail)
}

func doctorRenderChecks(checks []doctorCheck) []render.Check {
	out := make([]render.Check, 0, len(checks))
	for _, check := range checks {
		name := strings.TrimSpace(check.ID)
		if name == "" {
			name = strings.TrimSpace(check.Label)
		}
		out = append(out, render.Check{
			Name:    name,
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return out
}
