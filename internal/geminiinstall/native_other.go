//go:build !darwin

package geminiinstall

import (
	"context"
	"errors"
)

func SetupPermission(context.Context, string, bool) (string, error) {
	return "unavailable", errors.New("OS notification permission setup requires macOS")
}
