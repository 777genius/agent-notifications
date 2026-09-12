//go:build !darwin || !cgo

package notifier

import "errors"

type SystemBootClock struct{}

func (SystemBootClock) Now() (string, float64, error) {
	return "", 0, errors.New("qualified continuous clock unavailable")
}
