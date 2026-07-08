//go:build windows

package trawlkit

import (
	"os"
	"os/exec"
)

func configureChildCommand(cmd *exec.Cmd) {}

func signalChildProcess(cmd *exec.Cmd, signal os.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(signal)
}

func killChildProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
