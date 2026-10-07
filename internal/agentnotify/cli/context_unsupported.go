//go:build !linux && !darwin

package cli

import (
	"context"
	"errors"
)

// ReadSecureContext fails closed until a platform-specific ownership/ACL and
// opened-file identity implementation exists. No provenance is fabricated.
func ReadSecureContext(context.Context, string) ([]byte, error) {
	return nil, errors.New("secure_context_unsupported")
}
