//go:build windows

package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// The kernel-owned explicit policy carries this durable ownership marker, outside
// journal/state/cache. Started is written BEFORE Initialize can run. A retry of
// Started only calls Open, never Initialize. Ready pins the retained namespace.
// Directory identity is retained by the no-clobber stage -> state rename.
type ownership struct {
	Schema         int    `json:"schema"`
	InstallationID string `json:"installationID"`
	Token          string `json:"token"`
	DirectoryID    string `json:"directoryID"`
	Phase          string `json:"phase"`
	Namespace      string `json:"namespace"`
}

const stageName = ".setup-state"

func provision(ctx context.Context, o Options, s installruntime.PolicySnapshot, pre installruntime.Identity, result *Result) (installruntime.PolicySnapshot, installruntime.Identity, string, error) {
	root, e := openRoot(o.ControlRoot)
	if e != nil {
		return s, pre, "", fail("unsafe_state", e)
	}
	defer func() { _ = root.Close() }()
	var owner ownership
	raw, exists := s.Fields["setupState"]
	if exists {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&owner) != nil || owner.Schema != 1 || owner.InstallationID != s.Installation.Ledger.ID || len(owner.Token) != 64 || owner.DirectoryID == "" || (owner.Phase != "started" && owner.Phase != "ready") || (owner.Phase == "ready" && len(owner.Namespace) != 64) {
			return s, pre, "", fail("initialization_recovery_required", fmt.Errorf("invalid durable setupState ownership marker; preserve policy and state"))
		}
		if _, e = hex.DecodeString(owner.Token); e != nil {
			return s, pre, "", fail("initialization_recovery_required", e)
		}
	} else {
		if s.Policy.Enabled || s.Installation.Ledger.Enabled {
			return s, pre, "", fail("initialization_recovery_required", fmt.Errorf("enabled installation has no initialization ownership evidence"))
		}
		for _, name := range []string{"state", stageName} {
			if e = absent(root, name); e != nil {
				return s, pre, "", fail("initialization_recovery_required", fmt.Errorf("unowned %s: %w; preserve it for recovery", name, e))
			}
		}
		stage, e := mkdir(root, stageName)
		if e != nil {
			return s, pre, "", fail("unsafe_state", e)
		}
		id, e := directoryID(stage)
		_ = stage.Close()
		if e != nil {
			return s, pre, "", e
		}
		if e = o.fault("stage_created"); e != nil {
			return s, pre, "", fail("initialization_interrupted", e)
		}
		var token [32]byte
		if _, e = rand.Read(token[:]); e != nil {
			return s, pre, "", e
		}
		owner = ownership{Schema: 1, InstallationID: s.Installation.Ledger.ID, Token: hex.EncodeToString(token[:]), DirectoryID: id, Phase: "started"}
		s, pre, e = recordOwner(ctx, o, s, pre, owner, result)
		if e != nil {
			return s, pre, "", e
		}
		if e = o.fault("ownership_started"); e != nil {
			return s, pre, "", fail("initialization_interrupted", e)
		}
		// Only this uninterrupted invocation owns first initialization. The durable
		// marker prevents retries after any of the following partial/lost writes.
		stage, e = openChild(root, stageName)
		if e != nil {
			return s, pre, "", fail("unsafe_state", e)
		}
		if e = matches(stage, owner.DirectoryID); e != nil {
			_ = stage.Close()
			return s, pre, "", fail("unsafe_state", e)
		}
		j, e := mkdir(stage, "journal")
		if e != nil {
			_ = stage.Close()
			return s, pre, "", fail("initialization_interrupted", e)
		}
		_ = j.Close()
		spool, e := mkdir(stage, "native-spool")
		if e != nil {
			_ = stage.Close()
			return s, pre, "", fail("initialization_interrupted", e)
		}
		e = createLock(spool)
		_ = spool.Close()
		_ = stage.Close()
		if e != nil {
			return s, pre, "", fail("initialization_interrupted", e)
		}
		if e = o.fault("before_initialize"); e != nil {
			return s, pre, "", fail("initialization_interrupted", e)
		}
		if _, e = journal.Initialize(ctx, journal.Options{Root: filepath.Join(o.ControlRoot, stageName, "journal"), Clock: o.JournalClock}); e != nil {
			return s, pre, "", fail("initialization_interrupted", e)
		}
		if e = o.fault("journal_initialized"); e != nil {
			return s, pre, "", fail("initialization_interrupted", e)
		}
	}
	// Once a marker exists, loss of the expected directory/journal is never a
	// first install. A complete interrupted journal can be recovered via Open.
	location := "state"
	state, e := openChild(root, location)
	if os.IsNotExist(e) {
		location = stageName
		state, e = openChild(root, location)
	}
	if e != nil {
		return s, pre, "", fail("initialization_recovery_required", fmt.Errorf("expected owned state is missing or unsafe: %w", e))
	}
	defer func() { _ = state.Close() }()
	if e = matches(state, owner.DirectoryID); e != nil {
		return s, pre, "", fail("initialization_recovery_required", e)
	}
	if location == "state" {
		if e = absent(root, stageName); e != nil {
			return s, pre, "", fail("initialization_recovery_required", fmt.Errorf("unexpected provisioning stage: %w", e))
		}
	}
	spool, e := openChild(state, "native-spool")
	if e != nil {
		return s, pre, "", fail("initialization_recovery_required", e)
	}
	e = checkLock(spool)
	_ = spool.Close()
	if e != nil {
		return s, pre, "", fail("initialization_recovery_required", e)
	}
	store, e := journal.Open(ctx, journal.Options{Root: filepath.Join(o.ControlRoot, location, "journal"), Clock: o.JournalClock})
	if e != nil {
		return s, pre, "", fail("initialization_recovery_required", fmt.Errorf("expected journal cannot be opened; retain ownership marker and recover original state: %w", e))
	}
	ns := store.Namespace()
	if owner.Phase == "ready" && owner.Namespace != ns {
		return s, pre, "", fail("initialization_recovery_required", fmt.Errorf("journal namespace differs from durable ownership evidence"))
	}
	if owner.Phase == "started" {
		owner.Phase = "ready"
		owner.Namespace = ns
		s, pre, e = recordOwner(ctx, o, s, pre, owner, result)
		if e != nil {
			return s, pre, "", e
		}
		if e = o.fault("ownership_ready"); e != nil {
			return s, pre, "", fail("initialization_interrupted", e)
		}
	}
	if location == stageName {
		if e = absent(root, "state"); e != nil {
			return s, pre, "", fail("initialization_recovery_required", e)
		}
		named, e := openChild(root, stageName)
		if e != nil {
			return s, pre, "", fail("unsafe_state", e)
		}
		e = matches(named, owner.DirectoryID)
		_ = named.Close()
		if e != nil {
			return s, pre, "", fail("unsafe_state", e)
		}
		if e = renameExclusive(int(root.Fd()), stageName, "state"); e != nil {
			return s, pre, "", fail("initialization_recovery_required", e)
		}
		if e = syncOpenedDir(root); e != nil {
			return s, pre, "", fail("initialization_interrupted", e)
		}
	}
	if e = o.fault("state_published"); e != nil {
		return s, pre, "", fail("initialization_interrupted", e)
	}
	return s, pre, ns, nil
}

func recordOwner(ctx context.Context, o Options, s installruntime.PolicySnapshot, pre installruntime.Identity, owner ownership, result *Result) (installruntime.PolicySnapshot, installruntime.Identity, error) {
	// Initialization metadata must never restore eligibility before publication,
	// even if a manual policy edit opted in during an interrupted first setup.
	if s.Policy.Enabled || s.Installation.Ledger.Enabled {
		return s, pre, fail("initialization_recovery_required", fmt.Errorf("disable inconsistent intent before resuming first initialization"))
	}
	raw, _ := json.Marshal(owner)
	k := o.kernel(s.Installation.Ledger.Generation)
	k.ExpectedPolicy = &pre
	k.PolicyFields = map[string]json.RawMessage{"setupState": raw}
	// Reservation preserves disabled intent and passes normal writer-floor/CAS.
	l, e := installruntime.Commit(ctx, k)
	if e != nil {
		return s, pre, commitError(e)
	}
	result.Generation = l.Generation
	next, id, e := readSnapshot(ctx, o.ControlRoot)
	if e == nil && next.Installation.Ledger.Generation != l.Generation {
		e = fmt.Errorf("installation changed during provisioning; reread setup status")
	}
	if e != nil {
		return s, pre, fail("generation_changed", e)
	}
	return next, id, nil
}

// checkProvisioned performs only descriptor-relative structural reads under the
// final component/config locks. It never takes a journal lock or parses history.
// The full journal Open already completed under the separate setup lock.
func checkProvisioned(o Options, s installruntime.PolicySnapshot) error {
	var owner ownership
	if json.Unmarshal(s.Fields["setupState"], &owner) != nil || owner.Phase != "ready" {
		return fail("initialization_recovery_required", fmt.Errorf("durable ready ownership required"))
	}
	root, e := openRoot(o.ControlRoot)
	if e != nil {
		return fail("unsafe_state", e)
	}
	defer func() { _ = root.Close() }()
	state, e := openChild(root, "state")
	if e != nil {
		return fail("initialization_recovery_required", e)
	}
	defer func() { _ = state.Close() }()
	if e = matches(state, owner.DirectoryID); e != nil {
		return fail("initialization_recovery_required", e)
	}
	j, e := openChild(state, "journal")
	if e != nil {
		return fail("initialization_recovery_required", e)
	}
	defer func() { _ = j.Close() }()
	for _, name := range []string{"namespace", "journal.json", "lock"} {
		f, e := privateFile(j, name, 8*1024*1024)
		if e != nil {
			return fail("initialization_recovery_required", e)
		}
		if name == "namespace" {
			b := make([]byte, 66)
			n, err := f.Read(b)
			if err != nil || string(b[:n]) != owner.Namespace+"\n" {
				_ = f.Close()
				return fail("initialization_recovery_required", fmt.Errorf("namespace changed before policy commit"))
			}
		}
		_ = f.Close()
	}
	spool, e := openChild(state, "native-spool")
	if e != nil {
		return fail("initialization_recovery_required", e)
	}
	defer func() { _ = spool.Close() }()
	if e = checkLock(spool); e != nil {
		return fail("initialization_recovery_required", e)
	}
	return nil
}

func checkPolicy(root string) error {
	dir, e := openRoot(root)
	if e != nil {
		return e
	}
	defer func() { _ = dir.Close() }()
	f, e := privateFile(dir, "agent-notifications.json", 64*1024)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	return f.Close()
}
