package main

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func privateBootstrapProgressFixture(t *testing.T, stage, dir, path string) {
	t.Helper()
	for _, owned := range []string{stage, dir, path} {
		if err := installruntime.RestrictPrivatePath(owned); err != nil {
			t.Fatal(err)
		}
	}
}
