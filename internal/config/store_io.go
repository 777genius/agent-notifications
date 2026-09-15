package config

import "io"

type durableOutput interface {
	io.Writer
	Sync() error
	Close() error
}

// finishArtifact reports every write, flush, and close failure. Short writes
// are failures even if a broken adapter returns a nil error.
func finishArtifact(f durableOutput, data []byte) error {
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
