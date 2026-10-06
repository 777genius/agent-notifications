// Package cursorinstall supplies only the vendor mechanics for the explicitly
// selected Cursor user Stop route. UAP owns intent persistence, physical callback
// fences and acknowledgement; AN's existing owners retain consent and delivery.
package cursorinstall

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/profileauthority"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/cursor"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/cursorhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/ports"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/usecase"
)

const cursorVersion = "2026.09.28-64d2043"

// Authority is supplied by the composition owner after qualifying the current
// Linux amd64 Cursor tuple. QualificationID refers to that evidence, not a
// discovery hint. ExecutableDigest binds the owned observer bytes; Selector is
// the fixed portable binding path. No consent or physical token is fabricated.
type Authority struct {
	ProfileRoot, CursorVersion, QualificationID string
	Executable, ExecutableDigest, Selector      string
	ObjectID                                    string
}

// Adapter embeds the public historical Cursor capabilities. A zero requested
// selection retains its MCP/skill preparation lifecycle. No adapter is registered
// here; the consumer composition remains a separate change.
type Adapter struct {
	*cursor.Adapter
	native nativeconfig.Kernel
	paths  ports.PathPolicy
	fixed  Authority
}

// New uses only the existing public native kernel/path port and fixed facts.
// Canonicalization happens once at construction. Subsequent operations compare
// that original root and revalidate recorded tokens; they never select a new root.
func New(native nativeconfig.Kernel, paths ports.PathPolicy, fixed *Authority) (*Adapter, error) {
	if fixed == nil || paths == nil {
		return nil, fmt.Errorf("cursor selected route requires fixed qualified authority and path policy")
	}
	if err := native.RequireFileIO(); err != nil {
		return nil, err
	}
	a := &Adapter{Adapter: cursor.New(), native: native, paths: paths, fixed: *fixed}
	root, err := a.Adapter.ResolveProfileRoot(fixed.ProfileRoot)
	if err != nil {
		return nil, err
	}
	a.fixed.ProfileRoot = root
	if err := a.qualified(); err != nil {
		return nil, err
	}
	if err := a.verifyInvocation(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Adapter) qualified() error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || a.fixed.CursorVersion != cursorVersion || a.fixed.QualificationID == "" {
		return fmt.Errorf("cursor selected tuple is unqualified")
	}
	for _, value := range []string{a.fixed.QualificationID, a.fixed.ObjectID, a.fixed.ProfileRoot, a.fixed.Selector} {
		if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return fmt.Errorf("cursor fixed authority is incomplete")
		}
	}
	if !filepath.IsAbs(a.fixed.Selector) || filepath.Clean(a.fixed.Selector) != a.fixed.Selector {
		return fmt.Errorf("cursor requires a fixed absolute portable selector")
	}
	_, err := cursorhooks.RenderArgv(cursorhooks.LinuxUserShell32212,
		[]string{a.fixed.Executable, "cursor-event", "stop", "--binding", a.fixed.Selector})
	return err
}

func (a *Adapter) spec() cursorhooks.HookSpec {
	return cursorhooks.HookSpec{Executable: a.fixed.Executable, Selector: a.fixed.Selector}
}

func (a *Adapter) facts(canonical string) domain.CursorDeliveryFacts {
	return domain.CursorDeliveryFacts{ProfileRoot: a.fixed.ProfileRoot,
		HooksPath: filepath.Join(a.fixed.ProfileRoot, "hooks.json"), ProfileIdentity: a.fixed.ProfileRoot,
		CursorVersion: a.fixed.CursorVersion, TargetOS: "linux", TargetArch: "amd64", QualificationID: a.fixed.QualificationID,
		Executable: a.fixed.Executable, Selector: a.fixed.Selector, Shell: string(cursorhooks.LinuxUserShell32212),
		ObjectID: a.fixed.ObjectID, CanonicalDigest: canonical}
}

func (a *Adapter) ResolveProfileRoot(root string) (string, error) {
	resolved, err := a.Adapter.ResolveProfileRoot(root)
	if err != nil {
		return "", err
	}
	if resolved != a.fixed.ProfileRoot {
		return "", fmt.Errorf("cursor profile differs from original canonical root")
	}
	return resolved, nil
}

func (a *Adapter) CaptureProfileAuthority(ctx context.Context, c domain.DetectedClient) (domain.ProfileAuthority, error) {
	if err := a.qualified(); err != nil {
		return domain.ProfileAuthority{}, err
	}
	// The base detector probes the editor; its Version is not the qualified
	// agent version. The composition's fixed current tuple supplies that fact.
	if c.ClientID != domain.ClientCursor || c.ConfigRoot != a.fixed.ProfileRoot {
		return domain.ProfileAuthority{}, fmt.Errorf("cursor capture requires the qualified current tuple and original root")
	}
	return profileauthority.Capture(ctx, a.fixed.ProfileRoot)
}

func (a *Adapter) RevalidateProfileAuthority(ctx context.Context, id domain.ClientID, token domain.ProfileAuthority) error {
	if err := a.qualified(); err != nil {
		return err
	}
	if id != domain.ClientCursor || token.IsZero() || token.Facts().CanonicalRoot != a.fixed.ProfileRoot {
		return fmt.Errorf("cursor recorded physical authority is missing or differs")
	}
	return profileauthority.Revalidate(ctx, token)
}

// Check the packet's closed static facts; the per-attempt receipt and raw basis
// remain entirely in the requested plan, never in a stale adapter/client field.
func (a *Adapter) recorded(selected domain.SelectedDelivery) (domain.CursorDeliveryFacts, error) {
	if err := selected.Validate(); err != nil {
		return domain.CursorDeliveryFacts{}, err
	}
	f, ok := selected.CursorFacts()
	if !ok {
		return f, fmt.Errorf("cursor selected Stop packet required")
	}
	expected := a.facts(f.CanonicalDigest)
	expected.EntryDigest, expected.PlannedReceipt = f.EntryDigest, f.PlannedReceipt
	expected.OriginalExists, expected.OriginalRawDigest = f.OriginalExists, f.OriginalRawDigest
	expected.ProjectionDigest = f.ProjectionDigest
	if f != expected {
		return f, fmt.Errorf("cursor recorded fixed authority differs")
	}
	return f, a.qualified()
}

func (a *Adapter) requireToken(c domain.DetectedClient) error {
	if c.ClientID != domain.ClientCursor || c.ConfigRoot != a.fixed.ProfileRoot || c.ProfileAuthority == nil || c.ProfileAuthority.IsZero() || c.ProfileAuthority.Facts().CanonicalRoot != a.fixed.ProfileRoot {
		return fmt.Errorf("cursor selected route requires recorded physical profile authority")
	}
	// UAP owns revalidation before/after callbacks. This is only a missing-token
	// and exact-root admission check, not a second authority pipeline.
	return nil
}

func (a *Adapter) verifyInvocation() error {
	for _, path := range []string{a.fixed.Executable, a.fixed.Selector} {
		if err := a.paths.RequireContainedChild(string(filepath.Separator), path); err != nil {
			return err
		}
	}
	exe, err := a.native.ReadExactFile(a.fixed.Executable)
	if err != nil {
		return err
	}
	if !exe.Exists || exe.Mode&0111 == 0 || digest(exe.Body) != a.fixed.ExecutableDigest {
		return fmt.Errorf("cursor owned observer executable differs or is unavailable")
	}
	return nil
}

func digest(body []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(body)) }

func valueReceipt(r *cursorhooks.Receipt) domain.CursorHookReceipt {
	if r == nil {
		return domain.CursorHookReceipt{}
	}
	return domain.CursorHookReceipt{Version: r.Version, Event: r.Event, Executable: r.Spec.Executable,
		Selector: r.Spec.Selector, Shell: string(r.Shell), EntryDigest: r.EntryDigest, RemainderDigest: r.RemainderDigest}
}

func pureReceipt(r domain.CursorHookReceipt) *cursorhooks.Receipt {
	return &cursorhooks.Receipt{Version: r.Version, Event: r.Event,
		Spec: cursorhooks.HookSpec{Executable: r.Executable, Selector: r.Selector}, Shell: cursorhooks.ShellContract(r.Shell),
		EntryDigest: r.EntryDigest, RemainderDigest: r.RemainderDigest}
}

var (
	_ clients.Adapter                  = (*Adapter)(nil)
	_ clients.ProfileResolver          = (*Adapter)(nil)
	_ clients.PlanRefiner              = (*Adapter)(nil)
	_ clients.PhysicalProfileAuthority = (*Adapter)(nil)
	_ clients.Lifecycle                = (*Adapter)(nil)
	_ clients.ActivationPreflighter    = (*Adapter)(nil)
	_ clients.AutomaticActivator       = (*Adapter)(nil)
	_ clients.ReadOnlyVerifier         = (*Adapter)(nil)
	_ clients.RegistryInspector        = (*Adapter)(nil)
	_ usecase.NativeIntentReconciler   = (*Adapter)(nil)
)
