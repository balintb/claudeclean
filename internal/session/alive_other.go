//go:build !unix

package session

// No way to probe processes here, so fail safe and assume alive.
func ProcessAlive(pid int) bool {
	return pid > 0
}
