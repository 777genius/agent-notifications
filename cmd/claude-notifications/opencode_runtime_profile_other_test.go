//go:build !linux && !windows && (!darwin || !cgo)

package main

import (
	"context"
	"testing"
)

func TestRuntimeUnsupportedPortAlwaysDenied(t *testing.T) {
	in := runtimeProfileInput{Protocol: 1, Entry: "serve", NativePID: 1}
	if _, e := verifyRuntimeLiveImage(context.Background(), in); e == nil {
		t.Fatal("unsupported image proof succeeded")
	}
	if h, e := holdRuntimeLiveImage(context.Background(), in); e == nil || h != nil {
		t.Fatal("unsupported held proof succeeded")
	}
}
