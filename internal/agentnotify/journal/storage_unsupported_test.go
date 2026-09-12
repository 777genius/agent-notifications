//go:build !linux && !darwin

package journal

import (
	"context"
	"errors"
	"testing"
)

func TestUnsupportedExplicitUnavailable(t *testing.T) {
	if _, e := Open(context.Background(), Options{Root: t.TempDir()}); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if _, e := Initialize(context.Background(), Options{Root: t.TempDir()}); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if (PlatformClock{}).Sample().Available {
		t.Fatal("unsupported clock")
	}
}
