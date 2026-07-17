//go:build !windows

package trawlkit

import (
	"os"
	"os/exec"
	"syscall"
)

func configureChildCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalChildProcess(cmd *exec.Cmd, signal os.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	if sig, ok := signal.(syscall.Signal); ok {
		return syscall.Kill(-cmd.Process.Pid, sig)
	}
	return cmd.Process.Signal(signal)
}

func killChildProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func closeParentLifetimeOnExec(fd int) {
	syscall.CloseOnExec(fd)
}

func terminateOrphanedChild() {
	_ = syscall.Kill(-os.Getpid(), syscall.SIGKILL)
	os.Exit(1)
}
