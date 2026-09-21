//go:build !darwin || !cgo

package main

import (
	"context"
	"testing"
)

func sendQueuedNativeFromActive(t *testing.T, _ context.Context, _, _, _ string) {
	t.Helper()
}

func launchGenerationAfterColdStart(t *testing.T, _ context.Context, _ string) {
	t.Helper()
}
