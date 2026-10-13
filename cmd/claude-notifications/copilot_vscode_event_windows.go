//go:build windows

package main

import (
	"io"
	"os"
)

// Go's Windows pipe Close calls CancelIoEx before waiting for outstanding IO.
// Refuse consoles and other uncancelable input; Local uses an owned stdin pipe.
func prepareLocalInput(input io.ReadCloser) (io.ReadCloser, bool) {
	f, ok := input.(*os.File)
	if !ok {
		return input, true
	}
	info, err := f.Stat()
	return input, err == nil && info.Mode()&os.ModeNamedPipe != 0
}
