//go:build !windows

package searchworker

import "os/exec"

func configureProcess(_ *exec.Cmd) {
}

func terminateProcess(command *exec.Cmd) {
	if command == nil || command.Process == nil || command.ProcessState != nil {
		return
	}
	_ = command.Process.Kill()
}
