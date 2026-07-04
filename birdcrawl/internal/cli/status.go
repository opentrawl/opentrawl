package cli

import (
	"flag"
	"io"
)

func (r *runtime) runStatus(args []string) error {
	fs := flag.NewFlagSet("birdcrawl status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	return r.print(r.statusEnvelope())
}
