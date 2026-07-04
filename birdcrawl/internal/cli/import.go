package cli

import (
	"errors"
	"flag"
	"io"
	"os"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func (r *runtime) runImport(args []string) error {
	if len(args) == 0 || args[0] != "archive" {
		return usageErr(errors.New("import takes archive PATH"))
	}
	fs := flag.NewFlagSet("birdcrawl import archive", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args[1:]); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 1 {
		return usageErr(errors.New("import archive takes exactly one path"))
	}
	path := fs.Arg(0)
	if _, err := os.Stat(path); err != nil {
		return r.contractError("import_source_missing",
			"X archive not found at "+path,
			"Pass the path to your X data export .zip or its unzipped folder.")
	}
	return r.withStore(func(st *store.Store) error {
		result, err := archive.Importer{}.Import(r.ctx, st, path)
		if err != nil {
			return err
		}
		return r.print(newImportEnvelope(result.Stats))
	})
}
