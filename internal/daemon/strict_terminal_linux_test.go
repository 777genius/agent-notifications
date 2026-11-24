//go:build linux

package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/notification"
)

func TestParseProcBirthHandlesParentheses(t *testing.T) {
	fields := append([]string{"S", "42"}, make([]string, 18)...)
	for i := 2; i < len(fields); i++ {
		fields[i] = "0"
	}
	fields[19] = "91723"
	got, err := parseProcBirth([]byte("123 (terminal (a b)) " + strings.Join(fields, " ")))
	if err != nil || got.parent != 42 || got.start != 91723 {
		t.Fatalf("parsed %v, error=%v", got, err)
	}
	for _, sample := range []string{"", "123 broken", "1 (broken) S 2", "1 (a) S bad"} {
		if _, err := parseProcBirth([]byte(sample)); err == nil {
			t.Fatalf("accepted malformed proc stat %q", sample)
		}
	}
}

func TestOwnProcessAncestorHasStableBirth(t *testing.T) {
	self, err := procIdentity(os.Getpid())
	if err != nil || self.start == 0 {
		t.Fatal("missing current process identity", err)
	}
	if start, ok := ownProcessAncestor(os.Getpid()); !ok || start != self.start {
		t.Fatalf("own PID rejected: birth=%d match=%v", start, ok)
	}
	if _, ok := ownProcessAncestor(0); ok {
		t.Fatal("invalid PID accepted")
	}
}

func TestStrictTerminalNeverFollowsUnverifiedWindow(t *testing.T) {
	t.Setenv("DISPLAY", ":199")
	t.Setenv("WINDOWID", "999")
	for _, target := range []*notification.TerminalTarget{
		nil,
		{Agent: "opencode", Display: ":199", WindowID: "999", OwnerPID: os.Getpid(), OwnerBirth: 1},
		{Agent: "qoder", Display: "remote:0", WindowID: "999", OwnerPID: os.Getpid(), OwnerBirth: 1},
		{Agent: "qoder", Display: ":199", WindowID: "999", OwnerPID: os.Getpid(), OwnerBirth: 1},
	} {
		if StrictTerminalAuthorized(context.Background(), target) {
			t.Fatal("unverified terminal granted click action", target)
		}
	}
}

func TestStrictPeerCredentialsBindRealTerminalAncestry(t *testing.T) {
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "test-socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	client, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	pid, ok := strictPeer(server)
	if !ok || pid != os.Getpid() {
		t.Fatalf("kernel peer identity differs: %v, %v", pid, ok)
	}
	self, err := procIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	target := &notification.TerminalTarget{OwnerPID: os.Getpid(), OwnerBirth: self.start}
	if !strictPeerMatchesOrigin(server, target) {
		t.Fatal("kernel-authorized originating process denied")
	}
	target.OwnerBirth++
	if strictPeerMatchesOrigin(server, target) {
		t.Fatal("reused PID with mismatching birth was authorized")
	}
	target.OwnerBirth--
	target.OwnerPID = os.Getpid() + 1000000
	if strictPeerMatchesOrigin(server, target) {
		t.Fatal("unrelated process was accepted as the terminal")
	}
}
