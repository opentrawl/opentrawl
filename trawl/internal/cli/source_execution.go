package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func (r *Runtime) sourceExecutor() trawlkit.SourceExecutor {
	return trawlkit.NewSourceExecutor(trawlkit.SourceExecutorOptions{
		StateRoot: r.stateRoot,
		Timeout:   r.timeout,
		Verbosity: r.verbosity(),
		Stderr:    r.lockedStderr(),
	})
}

func errorsForMetadata(source Source) error {
	if source.MetadataErr != nil {
		return source.MetadataErr
	}
	return fmt.Errorf("%s is not registered", source.ID)
}

func sourceTimeout(command string) sourceTimeoutError {
	return sourceTimeoutError{command: command}
}

func sourceExecutionError(command string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return sourceTimeout(command)
	}
	return err
}
