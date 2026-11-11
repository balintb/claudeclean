//go:build !unix

package layout

import "errors"

func mkfifo(string) error { return errors.New("no fifos on this platform") }
