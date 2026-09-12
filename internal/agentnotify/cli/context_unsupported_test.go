//go:build !linux && !darwin

package cli

import (
	"context"
	"testing"
)

func TestSecureContextUnsupported(t *testing.T) {
	if b, e := ReadSecureContext(context.Background(), "context.json"); e == nil || b != nil {
		t.Fatal("unsupported secure reader must fail closed")
	}
}
