//go:build !windows

package trawlkit

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// parentLifetimePipe gives the hidden mutation child one read end whose only
// writer belongs to its supervising parent. If the parent is killed, EOF lets
// the child terminate instead of continuing as an orphan.
type parentLifetimePipe struct {
	read  *os.File
	write *os.File
	env   []string
}

func newParentLifetimePipe(cmd *exec.Cmd, env []string) (*parentLifetimePipe, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create parent lifetime pipe: %w", err)
	}
	childFD := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, read)
	return &parentLifetimePipe{
		read:  read,
		write: write,
		env:   setEnvValue(env, childParentFDEnv, strconv.Itoa(childFD)),
	}, nil
}

func (pipe *parentLifetimePipe) childStarted() {
	if pipe != nil && pipe.read != nil {
		_ = pipe.read.Close()
		pipe.read = nil
	}
}

func (pipe *parentLifetimePipe) Close() error {
	if pipe == nil {
		return nil
	}
	if pipe.read != nil {
		_ = pipe.read.Close()
		pipe.read = nil
	}
	if pipe.write == nil {
		return nil
	}
	err := pipe.write.Close()
	pipe.write = nil
	return err
}

func watchParentLifetime() (func(), error) {
	value := strings.TrimSpace(os.Getenv(childParentFDEnv))
	if value == "" {
		return nil, childWireEnvError{name: childParentFDEnv}
	}
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return nil, childWireEnvError{name: childParentFDEnv, invalid: true}
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil || stat.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		return nil, childWireEnvError{name: childParentFDEnv, invalid: true}
	}
	file := os.NewFile(uintptr(fd), "trawlkit-parent-lifetime")
	if file == nil {
		return nil, fmt.Errorf("open parent lifetime pipe")
	}
	closeParentLifetimeOnExec(fd)
	done := make(chan struct{})
	go func() {
		var buffer [1]byte
		_, _ = file.Read(buffer[:])
		select {
		case <-done:
			return
		default:
			terminateOrphanedChild()
		}
	}()
	return func() {
		close(done)
		_ = file.Close()
	}, nil
}
