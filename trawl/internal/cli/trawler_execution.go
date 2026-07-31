package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func (r *Runtime) trawlerExecutor() trawlkit.TrawlerExecutor {
	return trawlkit.NewTrawlerExecutor(trawlkit.TrawlerExecutorOptions{
		StateRoot: r.stateRoot,
		Timeout:   r.timeout,
		Verbosity: r.verbosity(),
		Stderr:    r.lockedStderr(),
	})
}

func trawlerDiscoveryFailure(trawler InstalledTrawler) error {
	if trawler.TrawlerDiscoveryError != nil {
		return trawler.TrawlerDiscoveryError
	}
	return fmt.Errorf("%s is not registered", installedTrawlerIdentityText(trawler))
}

func trawlerTimeout(command string) trawlerTimeoutError {
	return trawlerTimeoutError{command: command}
}

func trawlerExecutionError(command string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return trawlerTimeout(command)
	}
	return err
}
