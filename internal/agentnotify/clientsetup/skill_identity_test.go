package clientsetup

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestSkillIdentitiesEqualHostMode(t *testing.T) {
	want := installruntime.Identity{Exists: true, SHA256: "abc", Mode: 0600}
	got := installruntime.Identity{Exists: true, SHA256: "abc", Mode: installruntime.IdentityMode(0600)}
	if !skillIdentitiesEqual(got, want) {
		t.Fatal("recorded 0600 must match the host-retained private mode")
	}
	readonly := installruntime.Identity{Exists: true, SHA256: "abc", Mode: 0444}
	if skillIdentitiesEqual(readonly, want) && installruntime.IdentityMode(0444) != installruntime.IdentityMode(0600) {
		t.Fatal("readonly live identity must not match a writable recorded skill")
	}
	if skillIdentitiesEqual(installruntime.Identity{Exists: true, SHA256: "other", Mode: 0600}, want) {
		t.Fatal("digest mismatch accepted")
	}
}
