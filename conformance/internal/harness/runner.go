package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const commandTimeout = 30 * time.Second

type Runner struct {
	Binary  string
	Timeout time.Duration
}

type CommandOutput struct {
	Args     []string
	Stdout   []byte
	Stderr   []byte
	Err      error
	ExitCode int
	TimedOut bool
}

func NewRunner(binary string) Runner {
	return Runner{Binary: binary, Timeout: commandTimeout}
}

func (r Runner) Run(ctx context.Context, args ...string) CommandOutput {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = commandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, r.Binary, args...) // #nosec G204 -- conformance explicitly runs the binary provided by the caller.
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := CommandOutput{
		Args:     append([]string(nil), args...),
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		Err:      err,
		ExitCode: 0,
		TimedOut: errors.Is(runCtx.Err(), context.DeadlineExceeded),
	}
	if err == nil {
		return out
	}
	out.ExitCode = -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		out.ExitCode = exitErr.ExitCode()
	}
	return out
}

func (o CommandOutput) OK() bool {
	return o.Err == nil && !o.TimedOut
}

func (o CommandOutput) CombinedOutput() []byte {
	combined := make([]byte, 0, len(o.Stdout)+len(o.Stderr))
	combined = append(combined, o.Stdout...)
	combined = append(combined, o.Stderr...)
	return combined
}

func (o CommandOutput) FailureDetail() string {
	command := strings.Join(o.Args, " ")
	switch {
	case o.TimedOut:
		return fmt.Sprintf("%s timed out", command)
	case o.Err == nil:
		return ""
	case o.ExitCode >= 0:
		return fmt.Sprintf("%s exited %d", command, o.ExitCode)
	default:
		return fmt.Sprintf("%s could not start", command)
	}
}
