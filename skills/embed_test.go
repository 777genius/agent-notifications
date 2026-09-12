package skills

import (
	"bytes"
	"os"
	"testing"
)

func TestCanonicalEmbed(t *testing.T) {
	canonical, err := os.ReadFile("agent-notify/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	got := AgentNotify()
	if len(got) == 0 || !bytes.Equal(got, canonical) {
		t.Fatal("embed differs from canonical authored file")
	}
	got[0] ^= 0xff
	if !bytes.Equal(AgentNotify(), canonical) {
		t.Fatal("caller can mutate embedded source")
	}
}
