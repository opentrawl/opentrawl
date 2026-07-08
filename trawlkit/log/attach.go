package log

import (
	"errors"
	"strings"
)

// AttachRun opens a handle for an existing run without writing a second start
// line. It is for a re-exec child whose parent owns the run lifecycle.
func AttachRun(opts Options) (*Run, error) {
	return newRun(opts, false)
}

func newRun(opts Options, writeStart bool) (*Run, error) {
	run, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	if writeStart {
		if err := run.write(LevelInfo, "start", run.startMessage(), VisibilityInternal); err != nil {
			return nil, err
		}
	} else if strings.TrimSpace(opts.RunID) == "" {
		return nil, errors.New("run id is required when attaching to an existing run")
	}
	return run, nil
}
