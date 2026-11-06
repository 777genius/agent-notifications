//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
)

// Restrict new click routes to local X11; never forward focus to a remote
// display, an unrelated window with the same title, or a Wayland guess.
var localX11Display = regexp.MustCompile(`^:[0-9]{1,4}(\.[0-9]{1,2})?$`)

const strictFocusXdotool = "/usr/bin/xdotool"

type procBirth struct {
	parent int
	start  uint64
}

func parseProcBirth(data []byte) (procBirth, error) {
	// /proc/PID/stat field 2 is a parenthesized command name, possibly
	// containing spaces and parentheses; split only after its last ')'.
	i := strings.LastIndexByte(string(data), ')')
	if i < 0 {
		return procBirth{}, errors.New("invalid_proc_stat")
	}
	fields := strings.Fields(string(data[i+1:]))
	if len(fields) < 20 {
		return procBirth{}, errors.New("short_proc_stat")
	}
	parent, err := strconv.Atoi(fields[1]) // field 4
	if err != nil || parent < 0 {
		return procBirth{}, errors.New("invalid_proc_parent")
	}
	start, err := strconv.ParseUint(fields[19], 10, 64) // field 22
	if err != nil || start == 0 {
		return procBirth{}, errors.New("invalid_proc_birth")
	}
	return procBirth{parent: parent, start: start}, nil
}

func procIdentity(pid int) (procBirth, error) {
	if pid <= 1 {
		return procBirth{}, errors.New("invalid_pid")
	}
	info, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	if err != nil {
		return procBirth{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return procBirth{}, errors.New("foreign_process")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procBirth{}, err
	}
	return parseProcBirth(data)
}

// policyProof is an unlocked bounded reader: the original Qoder hook may
// already hold the installer policy lease while the shared daemon submits the
// banner. A second lease here would deadlock. Validate stable bytes around
// the read and require the same digest again when the click arrives.
func policyProof(controlRoot string) (string, bool) {
	path := filepath.Join(controlRoot, "agent-notifications.json")
	before, err := installruntime.Fingerprint(path)
	if err != nil || !before.Exists || before.Link != "" || before.SHA256 == "" {
		return "", false
	}
	policy, err := installruntime.ReadUserPolicy(controlRoot)
	if err != nil || !policy.Enabled {
		return "", false
	}
	after, err := installruntime.Fingerprint(path)
	return before.SHA256, err == nil && after == before
}

func ownProcessAncestor(pid int) (uint64, bool) {
	seen := make(map[int]bool)
	for current, depth := os.Getpid(), 0; current > 1 && depth < 80 && !seen[current]; depth++ {
		seen[current] = true
		info, err := procIdentity(current)
		if err != nil {
			return 0, false
		}
		if current == pid {
			return info.start, true
		}
		current = info.parent
	}
	return 0, false
}

func x11WindowOwner(ctx context.Context, display, id string) (int, error) {
	return xresWindowPID(ctx, display, id, "owner", "")
}

// CaptureStrictTerminal never uses source/model-provided session IDs, cwd or
// names. The actual hook must still be a child of the terminal owning WINDOWID.
// A delegated/remote/headless event is informational by default.
func CaptureStrictTerminal(ctx context.Context, cfg *config.Config, agent config.AgentID, controlRoot, installationID string, generation uint64) (*notification.TerminalTarget, bool) {
	if ctx == nil || ctx.Err() != nil || cfg == nil || !cfg.TerminalClickAuthorized() ||
		installationID == "" || generation == 0 || !filepath.IsAbs(controlRoot) || filepath.Clean(controlRoot) != controlRoot {
		return nil, false
	}
	selected, revision, ok := cfg.TerminalClickSelection()
	if !ok || !config.RecheckTerminalClickConsent(selected, revision, agent) {
		return nil, false
	}
	policySHA, ok := policyProof(controlRoot)
	if !ok {
		return nil, false
	}
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if os.Getenv(key) != "" {
			return nil, false
		}
	}
	if os.Getenv("XDG_SESSION_TYPE") == "wayland" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return nil, false
	}
	display, id := os.Getenv("DISPLAY"), os.Getenv("WINDOWID")
	if !localX11Display.MatchString(display) || id == "" {
		return nil, false
	}
	id, err := normalizeX11WindowID(id)
	if err != nil || id == "0" {
		return nil, false
	}
	pid, err := x11WindowOwner(ctx, display, id)
	if err != nil {
		return nil, false
	}
	birth, ok := ownProcessAncestor(pid)
	if !ok {
		return nil, false
	}
	nonce, err := windowNonce()
	if err != nil {
		return nil, false
	}
	// The per-window marker disappears when X11 destroys this window. It is
	// bounded and checked again at click time, so a reused numeric XID
	// cannot activate a later window in the same terminal process.
	markedPID, stableNonce, err := xresWindowRecord(ctx, display, id, "mark", nonce)
	if err != nil || markedPID != pid || stableNonce == "" {
		return nil, false
	}
	nonce = stableNonce
	return &notification.TerminalTarget{
		Agent: string(agent), Display: display, WindowID: id, WindowNonce: nonce,
		OwnerPID: pid, OwnerBirth: birth,
		ConfigPath: selected, ConfigRevision: revision,
		ControlRoot: controlRoot, InstallationID: installationID, Generation: generation,
		PolicySHA256: policySHA,
	}, true
}

// StrictTerminalAuthorized revalidates the immutable target at both native
// notification submission and click time. Never fall back to another window.
func StrictTerminalAuthorized(ctx context.Context, target *notification.TerminalTarget) bool {
	if ctx == nil || ctx.Err() != nil || target == nil ||
		target.OwnerPID <= 1 || target.OwnerBirth == 0 ||
		target.InstallationID == "" || target.Generation == 0 ||
		!filepath.IsAbs(target.ControlRoot) || filepath.Clean(target.ControlRoot) != target.ControlRoot ||
		!localX11Display.MatchString(target.Display) {
		return false
	}
	agent := config.AgentID(target.Agent)
	switch agent {
	case config.AgentQoder, config.AgentGemini, config.AgentCursor:
	default:
		return false
	}
	if !config.RecheckTerminalClickConsent(target.ConfigPath, target.ConfigRevision, agent) {
		return false
	}
	policySHA, ok := policyProof(target.ControlRoot)
	if !ok || policySHA != target.PolicySHA256 {
		return false
	}
	id, err := normalizeX11WindowID(target.WindowID)
	if err != nil || id == "0" || id != target.WindowID {
		return false
	}
	birth, err := procIdentity(target.OwnerPID)
	if err != nil || birth.start != target.OwnerBirth {
		return false
	}
	pid, err := xresWindowPID(ctx, target.Display, id, "check", target.WindowNonce)
	if err != nil || pid != target.OwnerPID {
		return false
	}
	snap, err := installruntime.ReadInstalledSnapshot(target.ControlRoot)
	if err != nil || snap.Recovery || !snap.Enabled ||
		snap.Ledger.ID != target.InstallationID ||
		snap.Ledger.Generation != target.Generation || snap.Ledger.PendingMutation != nil {
		return false
	}
	return ctx.Err() == nil
}

func ActivateStrictTerminal(ctx context.Context, target *notification.TerminalTarget) error {
	if !StrictTerminalAuthorized(ctx, target) {
		return errors.New("strict_terminal_revoked")
	}
	op, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(op, strictFocusXdotool, "windowactivate", "--sync", target.WindowID)
	cmd.Env = append(os.Environ(), "DISPLAY="+target.Display)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("strict_x11_activate: %w", err)
	}
	return nil
}
