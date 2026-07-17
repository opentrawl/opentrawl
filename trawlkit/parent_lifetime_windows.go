//go:build windows

package trawlkit

import "os/exec"

// Windows keeps the pre-existing child supervision behavior. ExtraFiles is
// unsupported there, so the Unix parent-liveness guarantee is not fabricated.
type parentLifetimePipe struct {
	env []string
}

func newParentLifetimePipe(_ *exec.Cmd, env []string) (*parentLifetimePipe, error) {
	return &parentLifetimePipe{env: env}, nil
}

func (*parentLifetimePipe) childStarted()  {}
func (*parentLifetimePipe) Close() error   { return nil }
func watchParentLifetime() (func(), error) { return func() {}, nil }
