//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

func processExists(pid int) error { return syscall.Kill(pid, 0) }
func signalPID(pid int, force bool) error {
	if force {
		return syscall.Kill(pid, syscall.SIGKILL)
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}
func detachCommand(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// Linux pidfds bind a signal to the original process, eliminating the PID
// reuse race between identity checking and signal delivery.
func openProcessHandle(pid int) (int, error) {
	const sysPIDFDOpen = 434
	fd, _, errno := syscall.Syscall(sysPIDFDOpen, uintptr(pid), 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func closeProcessHandle(fd int) {
	if fd >= 0 {
		_ = syscall.Close(fd)
	}
}

func signalProcessHandle(fd, pid int, sig syscall.Signal) error {
	if fd < 0 {
		return syscall.Kill(pid, sig)
	}
	const sysPIDFDSendSignal = 424
	_, _, errno := syscall.Syscall6(sysPIDFDSendSignal, uintptr(fd), uintptr(sig), 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
