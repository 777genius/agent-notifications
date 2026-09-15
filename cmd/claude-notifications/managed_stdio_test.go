package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/managedstdio"
)

func TestDispatchManagedStdioIgnoresOrdinaryCLI(t *testing.T) {
	var stderr bytes.Buffer
	handled, code := dispatchManagedStdio([]string{"setup-notifications", "wizard"}, &stderr)
	if handled || code != 0 || stderr.Len() != 0 {
		t.Fatalf("ordinary CLI: handled=%v code=%d stderr=%q", handled, code, stderr.String())
	}
}

func TestDispatchManagedStdioRejectsUnknownVersion(t *testing.T) {
	var stderr bytes.Buffer
	handled, code := dispatchManagedStdio([]string{"--internal-stdio-v2", "extra"}, &stderr)
	if !handled || code != 126 {
		t.Fatalf("unknown version: handled=%v code=%d", handled, code)
	}
	if !strings.Contains(stderr.String(), "unknown protocol version") {
		t.Fatalf("stderr: %s", stderr.String())
	}
}

func TestDispatchManagedStdioHandlesPrivateV1(t *testing.T) {
	var stderr bytes.Buffer
	handled, code := dispatchManagedStdio([]string{managedstdio.Mode}, &stderr)
	if !handled || code != 126 {
		t.Fatalf("v1: handled=%v code=%d stderr=%q", handled, code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "managed stdio:") {
		t.Fatalf("stderr: %s", stderr.String())
	}
	if strings.Contains(stderr.String(), "unknown protocol version") {
		t.Fatal("v1 treated as unknown version")
	}
}
