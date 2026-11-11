//go:build darwin && !cgo

package trash

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const trashCLI = "/usr/bin/trash"

type cliTrash struct{}

func New() Trasher { return cliTrash{} }

func (cliTrash) Trash(path string) (string, error) {
	if err := checkPath(path); err != nil {
		return "", err
	}
	if _, err := os.Stat(trashCLI); err != nil {
		return "", fmt.Errorf("move %s to Trash: %s not available: %w", path, trashCLI, err)
	}
	// path is absolute, so it can't be parsed as a flag.
	out, err := exec.Command(trashCLI, path).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("move %s to Trash: %v: %s", path, err, strings.TrimSpace(string(out)))
	}
	return "", nil
}
