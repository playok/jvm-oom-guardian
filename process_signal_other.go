//go:build !linux

package main

import "syscall"

func openProcessHandle(pid int) (int, error) { return -1, nil }
func closeProcessHandle(fd int)              {}
func signalProcessHandle(fd, pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}
