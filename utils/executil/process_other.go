//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package executil

import (
	"errors"
	"os"
	"os/exec"
)

func configureProcess(cmd *exec.Cmd) {}

func stopProcess(cmd *exec.Cmd) error {
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
