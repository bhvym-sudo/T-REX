//go:build windows

package searchworker

import (
	"os/exec"
	"strconv"
	"syscall"
)

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}

func terminateProcess(command *exec.Cmd) {
	if command == nil || command.Process == nil || command.ProcessState != nil {
		return
	}
	_ = exec.Command(
		"taskkill",
		"/PID", strconv.Itoa(command.Process.Pid),
		"/T",
		"/F",
	).Run()
}
