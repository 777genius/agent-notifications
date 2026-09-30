//go:build !darwin

package opencodeinstall

import (
	"context"
	"errors"
)

func PrepareNativeSpool(context.Context, string) (string, error) {
	return "", errors.New("OpenCode native spool requires macOS")
}

func SetupPermission(context.Context, string, bool) (string, error) {
	return "unavailable", errors.New("OpenCode native permission requires macOS")
}
