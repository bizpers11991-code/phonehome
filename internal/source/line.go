package source

import (
	"bufio"
	"errors"
)

// MaxLine is the longest log line a source parses, in bytes. Real lines are
// a few hundred bytes; the cap keeps a damaged or hostile log from making
// phonehome hold an unbounded line in memory.
const MaxLine = 64 << 10

// ReadLine reads the next line from br, including its newline, and returns
// it with the number of bytes it consumed. A line longer than MaxLine is
// read to its end but returned as nil, so the caller skips it while still
// counting n towards its offset. err is as for bufio.Reader.ReadSlice, except
// that it is never bufio.ErrBufferFull. The line is valid until the next read.
func ReadLine(br *bufio.Reader) (line []byte, n int, err error) {
	chunk, err := br.ReadSlice('\n')
	n = len(chunk)
	if !errors.Is(err, bufio.ErrBufferFull) {
		if n > MaxLine {
			return nil, n, err
		}
		return chunk, n, err
	}
	long := false
	line = append([]byte(nil), chunk...)
	for errors.Is(err, bufio.ErrBufferFull) {
		chunk, err = br.ReadSlice('\n')
		n += len(chunk)
		if long = long || n > MaxLine; long {
			line = nil
		} else {
			line = append(line, chunk...)
		}
	}
	if long {
		return nil, n, err
	}
	return line, n, err
}
