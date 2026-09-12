// Package clientsetup persists one managed MCP registration, without activation.
package clientsetup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

const Managed = "existing-installer"
const maxState = 16384

var ErrConflict = errors.New("client registration ownership conflict; reconcile managed/portable registration explicitly")
var ErrRecovery = errors.New("installer recovery required before client registration; use kernel recovery and reread generation")
var unchanged = errors.New("registration unchanged")

// Request is trusted installer input. All paths are explicit, absolute, clean,
// physical paths. Command must be a ledger-owned executable within RuntimeRoot.
// Mode is explicit: portable registration is a separate, unsupported owner.
// ExpectedGeneration is mandatory (zero is invalid); no runtime adoption occurs.
type Request struct {
	ControlRoot, RuntimeRoot, Command, ConfigPath string
	Provider                                      registration.Provider
	Mode                                          string
	ExpectedGeneration                            uint64
	Remove                                        bool
	SkillProjection                               *SkillProjection
}
type Result struct {
	Ledger     installruntime.Ledger
	ConsumerID string
	Changed    bool
}
type ownership struct {
	Schema                                                    int
	Mode, InstallationID, RuntimeRoot, ConfigPath, ConsumerID string
	Previous                                                  registration.Ownership
	Skill                                                     *skillOwnership `json:",omitempty"`
}

func clean(p string) bool {
	return utf8.ValidString(p) && len(p) <= 4096 && filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsRune(p, 0)
}
func within(root, p string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func consumerID(p registration.Provider, path string) string {
	h := sha256.Sum256([]byte(string(p) + "\x00" + path))
	return "agentnotify-mcp:" + string(p) + ":" + hex.EncodeToString(h[:])
}
func statePath(r Request) string {
	h := sha256.Sum256([]byte(consumerID(r.Provider, r.ConfigPath)))
	return filepath.Join(r.ControlRoot, "clientsetup-"+hex.EncodeToString(h[:])+".json")
}
func args(p registration.Provider) []string {
	return []string{"mcp-server", "--integration", string(p)}
}
func validate(r Request) error {
	if r.Mode != Managed || (r.Provider != registration.Codex && r.Provider != registration.Claude) || r.ExpectedGeneration == 0 || !clean(r.ControlRoot) || !clean(r.RuntimeRoot) || !clean(r.ConfigPath) || !clean(r.Command) || !within(r.RuntimeRoot, r.Command) || within(r.ControlRoot, r.ConfigPath) || r.ConfigPath == r.ControlRoot || within(r.RuntimeRoot, r.ConfigPath) || r.ConfigPath == r.RuntimeRoot {
		return ErrConflict
	}
	return installruntime.CheckPrivateControlRoot(r.ControlRoot)
}
func checkRuntime(r Request, s installruntime.InstalledSnapshot) error {
	l := s.Ledger
	if s.Recovery {
		return ErrRecovery
	}
	if l.ID == "" || l.Owner != Managed || l.RuntimeRoot != r.RuntimeRoot || l.Generation != r.ExpectedGeneration || len(l.Consumers) == 0 {
		return ErrConflict
	}
	registered := false
	for _, c := range l.Consumers {
		if c.RuntimeRoot == r.RuntimeRoot {
			registered = true
		}
	}
	if !registered {
		return ErrConflict
	}
	// Stable aliases are accepted only when every link and final executable are
	// ledger-owned, remain inside this runtime, and match kernel fingerprints.
	command := r.Command
	for depth := 0; ; depth++ {
		if depth >= 8 || !within(r.RuntimeRoot, command) {
			return ErrConflict
		}
		want, ok := l.Files[command]
		if !ok || !want.Exists {
			return ErrConflict
		}
		got, e := installruntime.Fingerprint(command)
		if e != nil {
			return e
		}
		if got != want {
			return ErrConflict
		}
		if want.Link == "" {
			if want.Mode&0111 == 0 {
				return ErrConflict
			}
			break
		}
		target := want.Link
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(command), target)
		}
		command = filepath.Clean(target)
	}
	if _, ok := l.Files[r.ConfigPath]; ok {
		return ErrConflict
	}
	return nil
}

// Apply does not execute the command, enable delivery, infer chat context, or
// establish permission readiness. A no-op leaves config, state and generation
// unchanged. The kernel owns all locking, writing, rollback and recovery.
func Apply(ctx context.Context, r Request) (Result, error) { return apply(ctx, r, nil) }

// Inspection reports validated ownership without creating locks or files.
type Inspection struct {
	Registered     bool
	SkillProjected bool
}

func Inspect(ctx context.Context, r Request) (Inspection, error) {
	var facts Inspection
	_, err := calculate(ctx, r, nil, &facts)
	return facts, err
}

func apply(ctx context.Context, r Request, fault func(string) error) (Result, error) {
	return calculate(ctx, r, fault, nil)
}
func calculate(ctx context.Context, r Request, fault func(string) error, inspection *Inspection) (Result, error) {
	var result Result
	if ctx == nil {
		return result, fmt.Errorf("installer context with deadline required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return result, fmt.Errorf("installer deadline required")
	}
	if e := ctx.Err(); e != nil {
		return result, e
	}
	if e := validate(r); e != nil {
		return result, e
	}
	s, e := installruntime.ReadInstalledSnapshot(r.ControlRoot)
	if e != nil {
		return result, e
	}
	if e = checkRuntime(r, s); e != nil {
		return result, e
	}
	if r.SkillProjection != nil {
		projection := *r.SkillProjection
		r.SkillProjection = &projection
	}
	configPaths := []string{r.ConfigPath}
	locked := false
	id := consumerID(r.Provider, r.ConfigPath)
	result.ConsumerID = id
	// Validate without creating locks; repeat the complete calculation in Prepare.
	prepare := func() ([]installruntime.File, error) {
		current, e := installruntime.ReadInstalledSnapshot(r.ControlRoot)
		if e != nil {
			return nil, e
		}
		if e = checkRuntime(r, current); e != nil {
			return nil, e
		}
		l := current.Ledger
		path := statePath(r)
		state, si, e := read(path, maxState)
		if e != nil {
			return nil, e
		}
		_, installed := l.Consumers[id]
		want, tracked := l.Files[path]
		if installed != tracked || installed != si.Exists || (tracked && (want != si || si.Mode != 0600)) {
			return nil, ErrConflict
		}
		var prev *registration.Ownership
		var previousSkill *skillOwnership
		if installed {
			var o ownership
			if strictjson.Validate(state, strictjson.Budget{Bytes: maxState, Depth: 8, Entries: 64}) != nil {
				return nil, ErrConflict
			}
			d := json.NewDecoder(bytes.NewReader(state))
			d.DisallowUnknownFields()
			if d.Decode(&o) != nil || (o.Schema != 1 && o.Schema != 2) || (o.Schema == 1 && o.Skill != nil) || (o.Schema == 2 && o.Skill == nil) || o.Mode != Managed || o.InstallationID != l.ID || o.RuntimeRoot != r.RuntimeRoot || o.ConfigPath != r.ConfigPath || o.ConsumerID != id || o.Previous.Provider != r.Provider || o.Previous.Server != registration.Server {
				return nil, ErrConflict
			}
			// encoding/json alone accepts case aliases and omitted zero-valued fields.
			// Compare the decoded shape to the complete versioned schema as well.
			canonical, _ := json.Marshal(o)
			var actualShape, expectedShape any
			if json.Unmarshal(state, &actualShape) != nil || json.Unmarshal(canonical, &expectedShape) != nil || !reflect.DeepEqual(actualShape, expectedShape) {
				return nil, ErrConflict
			}
			t := o.Previous.Transport
			if !clean(t.Command) || !within(r.RuntimeRoot, t.Command) || !t.ArgsPresent || !reflect.DeepEqual(t.Args, args(r.Provider)) || t.TypePresent != (r.Provider == registration.Claude) || (t.TypePresent && t.Type != "stdio") || (!t.TypePresent && t.Type != "") {
				return nil, ErrConflict
			}
			c := l.Consumers[id]
			if c.RuntimeRoot != r.RuntimeRoot || c.Registration != r.ConfigPath || !reflect.DeepEqual(c.Commands, []string{t.Command}) {
				return nil, ErrConflict
			}
			prev = &o.Previous
			previousSkill = o.Skill
		}
		if inspection != nil {
			inspection.Registered = installed
			inspection.SkillProjected = previousSkill != nil
		}
		skillFiles, nextSkill, skillPaths, e := projectSkill(r, l, previousSkill, inspection != nil)
		if e != nil {
			return nil, e
		}
		paths := append([]string{r.ConfigPath}, skillPaths...)
		if locked && !reflect.DeepEqual(paths, configPaths) {
			return nil, ErrConflict
		}
		if !locked {
			configPaths = paths
		}
		input, ci, e := read(r.ConfigPath, registration.MaxBytes)
		if e != nil {
			return nil, e
		}
		if ci.Exists && len(input) == 0 {
			return nil, registration.ErrInvalid
		}
		edited, e := registration.Apply(registration.Request{Provider: r.Provider, Input: input, Command: r.Command, Args: args(r.Provider), Previous: prev, Remove: r.Remove})
		if e != nil {
			return nil, e
		}
		files := skillFiles
		if edited.Changed {
			mode := uint32(0600)
			if ci.Exists {
				mode = ci.Mode
			}
			files = append(files, installruntime.File{Path: r.ConfigPath, Before: ci, Data: edited.Bytes, Mode: mode})
		}
		if r.Remove {
			// Final consumer cleanup already removes ledger-owned state and assets.
			if installed && len(l.Consumers) > 1 {
				files = append(files, installruntime.File{Path: path, Before: si, Remove: true})
			}
		} else {
			schema := 1
			if nextSkill != nil {
				schema = 2
			}
			out, e := json.Marshal(ownership{schema, Managed, l.ID, r.RuntimeRoot, r.ConfigPath, id, *edited.Owned, nextSkill})
			if e != nil || len(out) > maxState {
				return nil, ErrConflict
			}
			if !bytes.Equal(state, out) {
				files = append(files, installruntime.File{Path: path, Before: si, Data: out, Mode: 0600})
			}
		}
		if len(files) == 0 && (installed == !r.Remove) {
			return nil, unchanged
		}
		if r.Remove && !installed {
			return nil, unchanged
		}
		return files, nil
	}
	if _, e = prepare(); e != nil && !errors.Is(e, unchanged) {
		return result, e
	}
	if inspection != nil {
		return result, nil
	}
	// The kernel returns early for removal of an absent consumer, before its
	// generation check and Prepare. Fence that no-op result too; another installer
	// may have removed the consumer while this invocation waited for the lock.
	if fault != nil {
		if e := fault("before-commit"); e != nil {
			return result, e
		}
	}
	prepared := false
	lockedPrepare := func() ([]installruntime.File, error) { prepared = true; locked = true; return prepare() }
	l, e := installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, Owner: Managed, RuntimeRoot: r.RuntimeRoot, ConsumerID: id, Consumer: installruntime.Consumer{Registration: r.ConfigPath, Commands: []string{r.Command}}, RemoveConsumer: r.Remove, ExpectedGeneration: &r.ExpectedGeneration, ConfigPaths: configPaths, Prepare: lockedPrepare, Fault: fault})
	result.Ledger = l
	if errors.Is(e, unchanged) {
		return result, nil
	}
	if e != nil {
		return result, fmt.Errorf("client registration: %w", e)
	}
	if !prepared && l.Generation != r.ExpectedGeneration {
		return result, ErrConflict
	}
	result.Changed = prepared && l.Generation != s.Ledger.Generation
	return result, nil
}
