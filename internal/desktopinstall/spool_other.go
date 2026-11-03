//go:build !darwin

package desktopinstall

import (
	"context"
	"errors"
)

func PrepareNativeSpoolFor(context.Context, string, DesktopProduct) (string, error) {
	return "", errors.New("notification native spool requires macOS")
}
