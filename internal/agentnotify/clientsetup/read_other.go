//go:build !linux && !darwin

package clientsetup

import "github.com/777genius/agent-notifications/internal/installruntime"

// Other platforms require qualification of a bounded no-follow reader first.
func read(string, int) ([]byte, installruntime.Identity, error) {
	return nil, installruntime.Identity{}, ErrConflict
}

func requireDirectory(string) error { return ErrConflict }
