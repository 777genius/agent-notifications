//go:build !linux && !darwin

package main

import (
	"errors"
	"os"
)

func agentNotifyFile(*os.File) (*agentNotifyStream, error) {
	return nil, errors.New("unsupported_platform")
}
