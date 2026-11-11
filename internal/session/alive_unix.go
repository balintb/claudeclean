//go:build unix

package session

import (
	"errors"
	"syscall"
)

// EPERM means the process exists but belongs to someone else.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
