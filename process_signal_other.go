//go:build !linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func processExists(pid int) error {
	_, err := os.FindProcess(pid)
	return err
}
func signalPID(pid int, force bool) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(os.Kill)
}
func detachCommand(cmd *exec.Cmd) {}

func openProcessHandle(pid int) (int, error) { return -1, nil }
func closeProcessHandle(fd int)              {}
func signalProcessHandle(fd, pid int, sig syscall.Signal) error {
	return signalPID(pid, sig == syscall.SIGKILL)
}
