//go:build unix

package pgtest

import "syscall"

// processAlive reports whether a process with pid exists. EPERM means it exists
// but belongs to another user, so it still counts as alive.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
