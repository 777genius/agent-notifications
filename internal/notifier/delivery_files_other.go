//go:build !darwin && !linux

package notifier

import (
	"context"
	"errors"
	"os"
)

func openPrivate(string, bool, bool, bool) (*os.File, error) {
	return nil, errors.New("unsupported private filesystem")
}
func lockPrivate(context.Context, string, bool) (func(), error) {
	return nil, errors.New("unsupported private filesystem")
}
