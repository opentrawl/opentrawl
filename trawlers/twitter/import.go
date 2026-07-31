package twitter

import (
	"errors"
	"flag"
	"io"
	"os"

	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"github.com/opentrawl/opentrawl/twitter/internal/archive"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

func (r *runtime) runImportArchive(args []string) (*command.TrawlerCommandResponse, error) {
	fs := flag.NewFlagSet("twitter import archive", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return nil, usageErr(err)
	}
	if fs.NArg() != 1 {
		return nil, usageErr(errors.New("import archive takes exactly one path"))
	}
	path := fs.Arg(0)
	if _, err := os.Stat(path); err != nil {
		return nil, r.contractError("import_source_missing",
			"X archive not found at "+path)
	}
	var response *command.TrawlerCommandResponse
	err := r.withStore(func(st *store.Store) error {
		result, err := archive.Importer{}.Import(r.ctx, st, path)
		if err != nil {
			return err
		}
		response = twitterImportCommandResponse(newImportEnvelope(result.Stats))
		return nil
	})
	return response, err
}
