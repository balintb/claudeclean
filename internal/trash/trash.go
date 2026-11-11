// Package trash moves files to the macOS Trash. Nothing here deletes.
package trash

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type Trasher interface {
	Trash(path string) (string, error)
}

var ErrUnsupported = errors.New("moving to the Trash is only supported on macOS")

func checkPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("trash: refusing non-absolute or unclean path %q", path)
	}
	if strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("trash: refusing path with NUL byte %q", path)
	}
	if path == "/" {
		return fmt.Errorf("trash: refusing to trash the filesystem root")
	}
	return nil
}
