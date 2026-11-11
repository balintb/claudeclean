package session

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// lineReader skips lines longer than max instead of buffering them.
type lineReader struct {
	r   *bufio.Reader
	max int
	buf []byte
}

func newLineReader(r io.Reader, maxLine int) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, 64*1024), max: maxLine}
}

func (lr *lineReader) next() (line []byte, skipped bool, err error) {
	lr.buf = lr.buf[:0]
	tooLong := false
	for {
		chunk, err := lr.r.ReadSlice('\n')
		if !tooLong {
			if len(lr.buf)+len(chunk) > lr.max+2 { // +2 allows for "\r\n"
				tooLong = true
				lr.buf = lr.buf[:0]
			} else {
				lr.buf = append(lr.buf, chunk...)
			}
		}
		switch {
		case err == nil:
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(lr.buf) == 0 && !tooLong {
				return nil, false, io.EOF
			}
		default:
			return nil, false, err
		}
		if tooLong {
			return nil, true, nil
		}
		out := bytes.TrimRight(lr.buf, "\r\n")
		if len(out) > lr.max {
			return nil, true, nil
		}
		return out, false, nil
	}
}
