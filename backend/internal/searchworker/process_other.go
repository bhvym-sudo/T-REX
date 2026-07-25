//go:build !windows

package searchworker

import "os/exec"

func configureProcess(_ *exec.Cmd) {
}
