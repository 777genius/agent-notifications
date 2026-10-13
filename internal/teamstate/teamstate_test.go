package teamstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDetectTeamLead(t *testing.T) {
	// Create temp .claude directory with team config
	tmpDir := t.TempDir()
	teamsDir := filepath.Join(tmpDir, "teams", "test-team")
	if err := os.MkdirAll(teamsDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := teamConfig{
		Name:          "test-team",
		LeadSessionID: "session-123",
		Members: []teamMember{
			{AgentID: "team-lead@test-team", Name: "team-lead", AgentType: "team-lead"},
			{AgentID: "alice@test-team", Name: "alice", AgentType: "general-purpose"},
			{AgentID: "bob@test-team", Name: "bob", AgentType: "general-purpose"},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(teamsDir, "config.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(tmpDir)

	t.Run("matches lead session", func(t *testing.T) {
		info := mgr.DetectTeamLead("session-123")
		if info == nil {
			t.Fatal("expected team info, got nil")
			return
		}
		if info.TeamName != "test-team" {
			t.Errorf("expected team name 'test-team', got %q", info.TeamName)
		}
		if len(info.Members) != 2 {
			t.Errorf("expected 2 non-lead members, got %d", len(info.Members))
		}
		// Verify members are alice and bob (not team-lead)
		memberSet := map[string]bool{}
		for _, m := range info.Members {
			memberSet[m] = true
		}
		if !memberSet["alice"] || !memberSet["bob"] {
			t.Errorf("expected members [alice, bob], got %v", info.Members)
		}
	})

	t.Run("no match for different session", func(t *testing.T) {
		info := mgr.DetectTeamLead("session-999")
		if info != nil {
			t.Errorf("expected nil, got %+v", info)
		}
	})

	t.Run("no match for empty teams dir", func(t *testing.T) {
		emptyDir := t.TempDir()
		mgr2 := NewManager(emptyDir)
		info := mgr2.DetectTeamLead("session-123")
		if info != nil {
			t.Errorf("expected nil, got %+v", info)
		}
	})
}

func TestDetectTeamLead_NoNonLeadMembers(t *testing.T) {
	tmpDir := t.TempDir()
	teamsDir := filepath.Join(tmpDir, "teams", "solo-team")
	if err := os.MkdirAll(teamsDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := teamConfig{
		Name:          "solo-team",
		LeadSessionID: "session-solo",
		Members: []teamMember{
			{AgentID: "team-lead@solo-team", Name: "team-lead", AgentType: "team-lead"},
		},
	}
	data, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(teamsDir, "config.json"), data, 0644) //nolint:errcheck

	mgr := NewManager(tmpDir)
	info := mgr.DetectTeamLead("session-solo")
	if info != nil {
		t.Errorf("expected nil for team with no non-lead members, got %+v", info)
	}
}

func TestDetectTeamByName(t *testing.T) {
	tmpDir := t.TempDir()
	teamsDir := filepath.Join(tmpDir, "teams", "my-team")
	os.MkdirAll(teamsDir, 0755) //nolint:errcheck

	cfg := teamConfig{
		Name:          "my-team",
		LeadSessionID: "session-abc",
		Members: []teamMember{
			{AgentID: "team-lead@my-team", Name: "team-lead", AgentType: "team-lead"},
			{AgentID: "worker@my-team", Name: "worker", AgentType: "general-purpose"},
		},
	}
	data, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(teamsDir, "config.json"), data, 0644) //nolint:errcheck

	mgr := NewManager(tmpDir)

	info := mgr.DetectTeamByName("my-team")
	if info == nil {
		t.Fatal("expected team info")
		return
	}
	if len(info.Members) != 1 || info.Members[0] != "worker" {
		t.Errorf("expected members [worker], got %v", info.Members)
	}

	info = mgr.DetectTeamByName("nonexistent")
	if info != nil {
		t.Errorf("expected nil for nonexistent team, got %+v", info)
	}
}

func TestStateLifecycle(t *testing.T) {
	mgr := NewManager("")
	teamName := "test-lifecycle-" + t.Name()

	// Cleanup
	defer os.Remove(statePath(teamName))

	t.Run("fresh state", func(t *testing.T) {
		s, err := mgr.LoadState(teamName)
		if err != nil {
			t.Fatal(err)
		}
		if s.LeadStopped {
			t.Error("fresh state should have LeadStopped=false")
		}
		if len(s.IdleMembers) != 0 {
			t.Error("fresh state should have empty idle members")
		}
	})

	t.Run("record lead stopped", func(t *testing.T) {
		err := mgr.RecordLeadStopped(teamName)
		if err != nil {
			t.Fatal(err)
		}

		s, err := mgr.LoadState(teamName)
		if err != nil {
			t.Fatal(err)
		}
		if !s.LeadStopped {
			t.Error("expected LeadStopped=true after RecordLeadStopped")
		}
		if s.LeadStopAt == 0 {
			t.Error("expected LeadStopAt to be set")
		}
	})

	t.Run("record teammate idle", func(t *testing.T) {
		err := mgr.RecordTeammateIdle(teamName, "alice")
		if err != nil {
			t.Fatal(err)
		}

		s, err := mgr.LoadState(teamName)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := s.IdleMembers["alice"]; !ok {
			t.Error("expected alice in idle members")
		}
	})

	t.Run("check all idle - not all yet", func(t *testing.T) {
		allIdle, err := mgr.CheckAllIdle(teamName, []string{"alice", "bob"})
		if err != nil {
			t.Fatal(err)
		}
		if allIdle {
			t.Error("expected false: bob is not idle yet")
		}
	})

	t.Run("check all idle - all idle", func(t *testing.T) {
		mgr.RecordTeammateIdle(teamName, "bob") //nolint:errcheck

		allIdle, err := mgr.CheckAllIdle(teamName, []string{"alice", "bob"})
		if err != nil {
			t.Fatal(err)
		}
		if !allIdle {
			t.Error("expected true: lead stopped + both alice and bob idle")
		}
	})

	t.Run("mark notified resets state", func(t *testing.T) {
		err := mgr.MarkNotified(teamName)
		if err != nil {
			t.Fatal(err)
		}

		s, err := mgr.LoadState(teamName)
		if err != nil {
			t.Fatal(err)
		}
		if s.LeadStopped {
			t.Error("expected LeadStopped=false after MarkNotified (reset)")
		}
		if len(s.IdleMembers) != 0 {
			t.Error("expected empty idle members after MarkNotified (reset)")
		}
	})

	t.Run("prevents duplicate notification", func(t *testing.T) {
		// After MarkNotified, lead hasn't stopped again yet
		allIdle, err := mgr.CheckAllIdle(teamName, []string{"alice"})
		if err != nil {
			t.Fatal(err)
		}
		if allIdle {
			t.Error("expected false: lead hasn't stopped after reset")
		}
	})
}

func TestCheckAllIdle_LeadNotStopped(t *testing.T) {
	mgr := NewManager("")
	teamName := "test-lead-not-stopped"
	defer os.Remove(statePath(teamName))

	// Record teammate idle but NOT lead stopped
	mgr.RecordTeammateIdle(teamName, "alice") //nolint:errcheck

	allIdle, err := mgr.CheckAllIdle(teamName, []string{"alice"})
	if err != nil {
		t.Fatal(err)
	}
	if allIdle {
		t.Error("expected false: lead hasn't stopped")
	}
}

func TestCorruptedStateFile(t *testing.T) {
	mgr := NewManager("")
	teamName := "test-corrupted"
	path := statePath(teamName)
	defer os.Remove(path)

	// Write corrupted data
	os.WriteFile(path, []byte("not json {{{"), 0600) //nolint:errcheck

	s, err := mgr.LoadState(teamName)
	if err != nil {
		t.Fatal("expected no error for corrupted state (should return fresh)")
	}
	if s.LeadStopped {
		t.Error("corrupted state should return fresh state with LeadStopped=false")
	}
}

// Regression: competing hooks can both observe a ready team before either
// records the notification. Exactly one may claim it, and the reset must let
// a later completed cycle claim again.
func TestClaimAllIdleConcurrentAndNextCycle(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const teamName = "concurrent-claim"
	mgr := NewManager("")
	if err := mgr.RecordLeadStopped(teamName); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RecordTeammateIdle(teamName, "alice"); err != nil {
		t.Fatal(err)
	}

	const contenders = 32
	start := make(chan struct{})
	results := make(chan bool, contenders)
	errors := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := NewManager("").ClaimAllIdle(teamName, []string{"alice"})
			results <- claimed
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	claims := 0
	for claimed := range results {
		if claimed {
			claims++
		}
	}
	for err := range errors {
		if err != nil {
			t.Fatalf("claim failed: %v", err)
		}
	}
	if claims != 1 {
		t.Fatalf("claims = %d, want exactly one", claims)
	}

	s, err := mgr.LoadState(teamName)
	if err != nil {
		t.Fatal(err)
	}
	if s.LeadStopped || len(s.IdleMembers) != 0 || s.NotifiedAt == 0 {
		t.Fatalf("claimed state did not reset: %+v", s)
	}
	// The existing second-resolution notified_at fence intentionally prevents
	// a duplicate event in the same second from starting another completion.
	for time.Now().Unix() <= s.NotifiedAt {
		time.Sleep(10 * time.Millisecond)
	}
	if err := mgr.RecordLeadStopped(teamName); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RecordTeammateIdle(teamName, "alice"); err != nil {
		t.Fatal(err)
	}
	claimed, err := mgr.ClaimAllIdle(teamName, []string{"alice"})
	if err != nil || !claimed {
		t.Fatalf("next cycle claim = %t, err = %v; want true", claimed, err)
	}
}

// Regression: a failed persistence must never be reported as a successful
// claim, since that would send a notification without excluding another hook.
func TestClaimAllIdleSaveFailure(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const teamName = "claim-save-failure"
	mgr := NewManager("")
	if err := mgr.RecordLeadStopped(teamName); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RecordTeammateIdle(teamName, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath(teamName)+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	claimed, err := mgr.ClaimAllIdle(teamName, []string{"alice"})
	if err == nil || claimed {
		t.Fatalf("failed save returned claim = %t, err = %v", claimed, err)
	}
	s, err := mgr.LoadState(teamName)
	if err != nil {
		t.Fatal(err)
	}
	if !s.LeadStopped || len(s.IdleMembers) != 1 || s.NotifiedAt != 0 {
		t.Fatalf("failed claim changed persisted state: %+v", s)
	}
}
