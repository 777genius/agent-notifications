package installruntime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// The damaged-asset exception must never become an enabling, sibling-policy or
// filesystem mutation escape. Exercise Commit and verify its actual preimages.
func TestGeminiRevocationRejectsBroaderRequests(t *testing.T) {
	ctx, initial := request(t)
	initial.ConsumerID = "gemini-notifications"
	initial.Consumer.Registration = filepath.Join(initial.ControlRoot, "gemini-receipt.json")
	initial.Files = []File{{Path: initial.Consumer.Registration, Data: []byte("inert test receipt"), Mode: 0600}}
	l, err := Commit(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ReadRevocationSnapshot(ctx, initial.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(initial.Consumer.Registration); err != nil {
		t.Fatal(err)
	}
	base := Request{ControlRoot: initial.ControlRoot, RuntimeRoot: initial.RuntimeRoot,
		Owner: initial.Owner, ConsumerID: initial.ConsumerID, PolicyOnly: true, RefreshOnly: true, RevokeGemini: true,
		ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage}
	for name, change := range map[string]func(*Request){
		"enable": func(r *Request) {
			r.PolicyFields["route"] = json.RawMessage(`{"geminiNotifications":{"desktop":true,"webhook":false}}`)
		},
		"sibling": func(r *Request) {
			r.PolicyFields["route"] = json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false},"openCodeNotifications":{"desktop":false,"webhook":false}}`)
		},
		"rates": func(r *Request) { r.PolicyFields["rates"] = json.RawMessage(`{"burst":1}`) },
		"file":  func(r *Request) { r.Files = initial.Files },
		"prepare": func(r *Request) {
			r.Prepare = func() ([]File, error) { t.Fatal("revocation executed preparation"); return nil, nil }
		},
		"config":             func(r *Request) { r.ConfigPaths = []string{filepath.Join(initial.ControlRoot, "foreign.json")} },
		"global-disable":     func(r *Request) { off := false; r.PolicyEnabled = &off },
		"other-consumer":     func(r *Request) { r.ConsumerID = "opencode-notifications" },
		"both-bypasses":      func(r *Request) { r.RevokeOpenCode = true },
		"missing-policy-cas": func(r *Request) { r.ExpectedPolicy = nil },
		"missing-generation": func(r *Request) { r.ExpectedGeneration = nil },
		"wrong-owner":        func(r *Request) { r.Owner = "foreign-installer" },
		"not-policy-only":    func(r *Request) { r.PolicyOnly = false },
		"not-refresh-only":   func(r *Request) { r.RefreshOnly = false },
		"stale-policy-cas":   func(r *Request) { r.ExpectedPolicy = &Identity{Exists: true, SHA256: "stale"} },
		"stale-generation":   func(r *Request) { stale := s.Generation - 1; r.ExpectedGeneration = &stale },
		"runtime-relocation": func(r *Request) { r.RuntimeRoot = filepath.Join(initial.RuntimeRoot, "other") },
		"unclean-runtime":    func(r *Request) { r.RuntimeRoot = initial.RuntimeRoot + string(filepath.Separator) + "." },
		"relative-runtime":   func(r *Request) { r.RuntimeRoot = "runtime" },
		"recover":            func(r *Request) { r.RecoverOnly = true },
		"remove":             func(r *Request) { r.RemoveConsumer = true },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false}}`)}
			change(&r)
			if _, err := Commit(ctx, r); err == nil {
				t.Fatal("revocation admitted a broader request")
			}
			after, recovery, err := ReadOwnership(initial.ControlRoot)
			if err != nil || recovery || !reflect.DeepEqual(after, l) {
				t.Fatalf("refused request changed ledger: %+v %v", after, err)
			}
			policy, err := Fingerprint(filepath.Join(initial.ControlRoot, "agent-notifications.json"))
			if err != nil || policy != s.Preimage {
				t.Fatalf("refused request changed policy: %+v %v", policy, err)
			}
			if _, err := os.Lstat(initial.Consumer.Registration); !os.IsNotExist(err) {
				t.Fatal("refused request restored damaged asset")
			}
		})
	}
}

func TestGeminiRevocationRequiresExistingControlState(t *testing.T) {
	for _, state := range []string{"component-lock", "policy-lock", "pending-recovery", "unregistered", "empty-registration"} {
		t.Run(state, func(t *testing.T) {
			ctx, initial := request(t)
			initial.ConsumerID = "gemini-notifications"
			if state == "unregistered" {
				initial.ConsumerID = "test-sibling"
			}
			if state != "empty-registration" {
				initial.Consumer.Registration = filepath.Join(initial.ControlRoot, "gemini-receipt.json")
				initial.Files = []File{{Path: initial.Consumer.Registration, Data: []byte("inert receipt"), Mode: 0600}}
			}
			l, err := Commit(ctx, initial)
			if err != nil {
				t.Fatal(err)
			}
			s, err := ReadRevocationSnapshot(ctx, initial.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "component-lock":
				if err := os.Remove(filepath.Join(initial.ControlRoot, ".component-install.lock")); err != nil {
					t.Fatal(err)
				}
			case "policy-lock":
				if err := os.Remove(filepath.Join(initial.ControlRoot, "agent-notifications.json.lock")); err != nil {
					t.Fatal(err)
				}
			case "pending-recovery":
				fault := errors.New("interrupted transaction")
				initial.ExpectedGeneration = &l.Generation
				initial.Files = nil
				initial.Fault = func(phase string) error {
					if phase == "transaction" {
						return fault
					}
					return nil
				}
				if _, err := Commit(ctx, initial); !errors.Is(err, fault) {
					t.Fatalf("recovery fixture did not reach durable transaction: %v", err)
				}
			}
			paths := []string{"ownership.json", "agent-notifications.json", "transaction.json", ".component-install.lock", "agent-notifications.json.lock", "gemini-receipt.json"}
			before := make(map[string]Identity)
			for _, path := range paths {
				before[path], err = Fingerprint(filepath.Join(initial.ControlRoot, path))
				if err != nil {
					t.Fatal(err)
				}
			}
			revoke := Request{ControlRoot: initial.ControlRoot, RuntimeRoot: initial.RuntimeRoot,
				Owner: initial.Owner, ConsumerID: "gemini-notifications", PolicyOnly: true, RefreshOnly: true, RevokeGemini: true,
				ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage,
				PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false}}`)}}
			if _, err := Commit(ctx, revoke); err == nil {
				t.Fatal("revocation bypassed missing control state or pending recovery")
			} else if state == "pending-recovery" && !errors.Is(err, ErrPolicyRecovery) {
				t.Fatalf("pending recovery did not fence revocation: %v", err)
			}
			for _, path := range paths {
				after, err := Fingerprint(filepath.Join(initial.ControlRoot, path))
				if err != nil || after != before[path] {
					t.Fatalf("refused revoke changed %s: %+v %v", path, after, err)
				}
			}
		})
	}
}

// Exercise the production kernel with actual directory/symlink substitution.
// Restoring delivery assets after revocation must not restore old consent.
func TestGeminiRevocationAfterRuntimeSymlinkReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink substitution; Windows filesystem guards are qualified separately")
	}
	ctx, r := request(t)
	r.ConsumerID = "gemini-notifications"
	receipt := filepath.Join(r.ControlRoot, "gemini-receipt.json")
	asset := filepath.Join(r.RuntimeRoot, "inert-sender")
	r.Consumer = Consumer{Registration: receipt, Commands: []string{asset, "gemini-event"}}
	r.Files = []File{{Path: receipt, Data: []byte("inert receipt"), Mode: 0600}, {Path: asset, Data: []byte("inert sender"), Mode: 0700}}
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	l, err = Commit(ctx, Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: r.Owner,
		ConsumerID: r.ConsumerID, RefreshOnly: true, ExpectedGeneration: &l.Generation,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":true,"webhook":true},"foreign":{"keep":true}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	retained := r.RuntimeRoot + "-retained"
	if err := os.Rename(r.RuntimeRoot, retained); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(retained, r.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(retained, "foreign")
	if err := os.WriteFile(foreign, []byte("foreign retained asset"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := []string{receipt, filepath.Join(retained, "inert-sender"), foreign, r.RuntimeRoot}
	before := make(map[string]Identity)
	for _, path := range paths {
		before[path], err = Fingerprint(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	revoke := Request{ControlRoot: r.ControlRoot, RuntimeRoot: l.Consumers[r.ConsumerID].RuntimeRoot,
		Owner: r.Owner, ConsumerID: r.ConsumerID, PolicyOnly: true, RefreshOnly: true, RevokeGemini: true,
		ExpectedGeneration: &l.Generation, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false}}`)}}
	for _, strict := range []bool{false, true} {
		refused := revoke
		refused.RevokeGemini = strict
		if strict {
			refused.RuntimeRoot = retained
		}
		if _, err := Commit(ctx, refused); err == nil {
			t.Fatal("damaged runtime admitted ordinary policy mutation or an unrecorded revocation root")
		}
		policy, err := Fingerprint(filepath.Join(r.ControlRoot, "agent-notifications.json"))
		if err != nil || policy != s.Preimage {
			t.Fatal("refused mutation changed consent")
		}
	}
	next, err := Commit(ctx, revoke)
	if err != nil {
		data, readErr := os.ReadFile(filepath.Join(r.ControlRoot, "agent-notifications.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		t.Fatalf("revocation depends on live runtime identity: %v; policy=%s", err, data)
	}
	if next.Generation != l.Generation+1 || next.PolicyGeneration != l.PolicyGeneration+1 ||
		!reflect.DeepEqual(next.Consumers, l.Consumers) || !reflect.DeepEqual(next.Files, l.Files) || !reflect.DeepEqual(next.Native, l.Native) {
		t.Fatalf("revocation changed asset ownership or failed to advance generations: %+v", next)
	}
	data, err := os.ReadFile(filepath.Join(r.ControlRoot, "agent-notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Route struct {
			Gemini  struct{ Desktop, Webhook bool } `json:"geminiNotifications"`
			Foreign struct{ Keep bool }             `json:"foreign"`
		}
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Route.Gemini.Desktop || policy.Route.Gemini.Webhook || !policy.Route.Foreign.Keep {
		t.Fatalf("revocation left consent or changed foreign policy: %s", data)
	}
	for _, path := range paths {
		after, err := Fingerprint(path)
		if err != nil || after != before[path] {
			t.Fatalf("revocation changed retained asset %s: %+v %v", path, after, err)
		}
	}
	if err := os.Remove(r.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(retained, r.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	current, err := ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(current.Fields["route"], &policy.Route); err != nil {
		t.Fatal(err)
	}
	if policy.Route.Gemini.Desktop || policy.Route.Gemini.Webhook || !policy.Route.Foreign.Keep || current.Installation.Ledger.PolicyGeneration != next.PolicyGeneration {
		t.Fatal("restoring assets restored consent or policy generation")
	}
	if _, release, err := AcquirePolicyLease(ctx, r.ControlRoot, s); err == nil {
		release()
		t.Fatal("old snapshot became authorized after restoration")
	}
}
