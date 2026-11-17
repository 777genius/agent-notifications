//go:build !linux && !darwin && !windows

package sharedstate

import "errors"

type directory struct{}

func openDirectory(*Store, bool) (*directory, []string, error) {
	return nil, nil, errors.New("private shared endpoints unsupported on this platform")
}
func (*directory) close()                             {}
func (*directory) ensureLock(string) error            { return errors.New("unsupported platform") }
func (*directory) checkLock(string) error             { return errors.New("unsupported platform") }
func (*directory) read(string, int64) ([]byte, error) { return nil, errors.New("unsupported platform") }
func (*directory) write(string, []byte) error         { return errors.New("unsupported platform") }
func (*directory) remove(string) error                { return errors.New("unsupported platform") }
