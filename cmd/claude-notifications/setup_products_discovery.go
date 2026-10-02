package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/geminiinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/clientdetect"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/claude"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/gemini"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/opencode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodeplugin"
)

type productFact struct {
	ID, Label, Profile, Executable, Reason string
	Present, Selectable                    bool
	Evidence                               []string
}
type productEnvironment struct {
	Home, PATH, DefaultControlRoot string
	Config                         config.EnvSnapshot
	Values                         map[string]string
}

// Snapshot only profile authorities and PATH. Neither credentials nor content
// of agent configuration is needed to compose the four existing providers.
func captureProductEnvironment() (productEnvironment, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return productEnvironment{}, err
	}
	control, err := installruntime.ControlRoot()
	if err != nil {
		return productEnvironment{}, err
	}
	e := productEnvironment{Home: home, PATH: os.Getenv("PATH"), DefaultControlRoot: control, Config: config.SnapshotEnv(), Values: map[string]string{}}
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_HOME", "CODEX_HOME", "OPENCODE_CONFIG_DIR", "GEMINI_CLI_HOME", "XDG_CONFIG_HOME", "AGENT_NOTIFICATIONS_CONTROL_ROOT", "PATHEXT", "LOCALAPPDATA", "APPDATA", "ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		e.Values[key] = os.Getenv(key)
	}
	return e, nil
}

func productScopes(a setupProductsArgs, e productEnvironment) (map[string]string, error) {
	if !validProductPath(e.Home) || len(e.PATH) > 32768 || len(filepath.SplitList(e.PATH)) > 128 {
		return nil, errors.New("invalid home or PATH budget")
	}
	for _, entry := range filepath.SplitList(e.PATH) {
		if len(entry) > 4096 {
			return nil, errors.New("PATH entry budget exceeded")
		}
	}
	// Explicit authorities never disappear into detector defaults, even if an
	// unrelated product is being selected. Overrides stronger than env win.
	scopes := map[string]string{"home": e.Home}
	pairs := [][3]string{{"claude-config", "CLAUDE_CONFIG_DIR", ".claude"}, {"codex-home", "CODEX_HOME", ".codex"}, {"control-root", "AGENT_NOTIFICATIONS_CONTROL_ROOT", ""}}
	for _, p := range pairs {
		value := a.Scopes[p[0]]
		if value == "" {
			value = e.Values[p[1]]
		}
		if value == "" && p[0] == "claude-config" {
			value = e.Values["CLAUDE_HOME"]
		}
		if value == "" {
			if p[0] == "control-root" {
				value = e.DefaultControlRoot
			} else {
				value = filepath.Join(e.Home, p[2])
			}
		}
		if !validProductPath(value) {
			return nil, fmt.Errorf("invalid %s authority", p[0])
		}
		canonical, err := installruntime.CanonicalPath(value)
		if p[0] == "codex-home" {
			canonical, err = codex.New().ResolveProfileRoot(value)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p[0], err)
		}
		scopes[p[0]] = canonical
	}
	in := opencodeplugin.Input{HomeDir: e.Home, XDGConfigHome: e.Values["XDG_CONFIG_HOME"], Override: a.Scopes["opencode-config-dir"]}
	if in.Override == "" {
		in.Override = e.Values["OPENCODE_CONFIG_DIR"]
	}
	root, err := opencodeplugin.ResolveConfigRoot(in)
	if err != nil {
		return nil, err
	}
	scopes["opencode-config-dir"], err = installruntime.CanonicalPath(root)
	if err != nil {
		return nil, err
	}
	parent := a.Scopes["gemini-home"]
	if parent == "" {
		parent = e.Values["GEMINI_CLI_HOME"]
	}
	if a.Scopes["gemini-config-root"] == "" && parent != "" && !validProductPath(parent) {
		return nil, errors.New("invalid Gemini parent authority")
	}
	scopes["gemini-config-root"], err = geminiinstall.ResolveConfigRoot(geminiinstall.Request{HomeDir: e.Home, GeminiHome: parent, ConfigRoot: a.Scopes["gemini-config-root"]})
	if err != nil {
		return nil, err
	}
	for _, id := range productOrder {
		if p := a.Scopes[id+"-executable"]; p != "" {
			resolved, err := normalizeProductExecutable(p)
			if err != nil {
				return nil, fmt.Errorf("%s override: %w", id, err)
			}
			scopes[id+"-executable"] = resolved
		}
	}
	claudeMCPParent := e.Home
	if a.Scopes["claude-config"] != "" || e.Values["CLAUDE_CONFIG_DIR"] != "" {
		claudeMCPParent = scopes["claude-config"]
	}
	for _, key := range []string{"claude-config", "codex-home", "control-root", "opencode-config-dir", "gemini-config-root"} {
		info, err := os.Stat(scopes[key])
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil && !info.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", key)
		}
	}
	scopes["claude-mcp-config"] = filepath.Join(claudeMCPParent, ".claude.json")
	scopes["codex-mcp-config"] = filepath.Join(scopes["codex-home"], "config.toml")
	return scopes, nil
}

// Filesystem normalization over provider LookPath, never a second surface or
// binary-name registry and never a version/auth probe.
func normalizeProductExecutable(path string) (string, error) {
	if !validProductPath(path) {
		return "", fmt.Errorf("absolute executable required: %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !validProductPath(resolved) {
		return "", errors.New("resolved executable exceeds path authority bounds")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0) {
		return "", errors.New("not a usable regular executable")
	}
	return resolved, nil
}

func discoverProducts(ctx context.Context, a setupProductsArgs, e productEnvironment) ([]productFact, map[string]string, error) {
	scopes, err := productScopes(a, e)
	if err != nil {
		return nil, nil, productAuthorityError{err}
	}
	return discoverProductsWithDetector(ctx, a, e, scopes, clientdetect.NewOS(e.Home))
}

func discoverProductsWithDetector(ctx context.Context, a setupProductsArgs, e productEnvironment, scopes map[string]string, detector clientdetect.Detector) ([]productFact, map[string]string, error) {
	detector.Environment = map[string]string{}
	for k, v := range e.Values {
		detector.Environment[k] = v
	}
	detector.Environment["CLAUDE_CONFIG_DIR"] = scopes["claude-config"]
	detector.Environment["CODEX_HOME"] = scopes["codex-home"]
	registry, err := clients.NewRegistry(claude.New(), codex.New(), opencode.New(), gemini.New())
	if err != nil {
		return nil, nil, err
	}
	detector.Registry = registry
	type lookup struct {
		path string
		err  error
	}
	memo := map[string]lookup{}
	detector.LookPath = func(name string) (string, error) {
		if old, ok := memo[name]; ok {
			return old.path, old.err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		p := scopes[name+"-executable"]
		var err error
		if p == "" {
			p, err = lookupProductExecutable(name, e)
		}
		if err == nil {
			p, err = normalizeProductExecutable(p)
		}
		memo[name] = lookup{p, err}
		return p, err
	}
	observations, err := detector.Detect(ctx)
	if err != nil {
		return nil, nil, err
	}
	facts := make([]productFact, 0, 4)
	for _, id := range productOrder {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		key := id + "-config-dir"
		switch id {
		case "claude":
			key = "claude-config"
		case "codex":
			key = "codex-home"
		case "gemini":
			key = "gemini-config-root"
		}
		f := productFact{ID: id, Label: productLabels[id], Profile: scopes[key], Selectable: true}
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" || runtime.GOOS == "windows" && runtime.GOARCH == "arm64" {
			f.Selectable = false
			f.Reason = "unsupported release platform"
		}
		for _, o := range observations {
			if string(o.ClientID) != id {
				continue
			}
			if o.DetectionError != nil {
				f.Selectable = false
				f.Reason = o.DetectionError.Error()
			}
			for _, s := range o.Surfaces {
				if s.Detected {
					f.Evidence = append(f.Evidence, s.Evidence)
				}
			}
		}
		l := memo[id]
		if l.err != nil {
			if errors.Is(l.err, exec.ErrNotFound) || errors.Is(l.err, os.ErrNotExist) {
				if f.Reason == "" {
					f.Reason = "not found in selected PATH"
				}
			} else {
				f.Selectable = false
				f.Reason = l.err.Error()
			}
		} else if l.path != "" {
			f.Present = true
			f.Executable = l.path
			scopes[id+"-executable"] = l.path
		}
		facts = append(facts, f)
	}
	return facts, scopes, nil
}

// Lookup uses captured PATH/PATHEXT without mutating process environment. Names
// come solely from the providers; relative cwd entries never gain authority.
func lookupProductExecutable(name string, e productEnvironment) (string, error) {
	suffixes := []string{""}
	if runtime.GOOS == "windows" {
		suffixes = nil
		extensions := e.Values["PATHEXT"]
		if extensions == "" {
			extensions = ".COM;.EXE;.BAT;.CMD"
		}
		if len(extensions) > 4096 {
			return "", errors.New("PATHEXT budget exceeded")
		}
		for _, extension := range strings.Split(extensions, ";") {
			if !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, `/\`) {
				return "", errors.New("invalid PATHEXT")
			}
			suffixes = append(suffixes, extension)
		}
	}
	for _, dir := range filepath.SplitList(e.PATH) {
		for _, suffix := range suffixes {
			candidate := filepath.Join(dir, name+suffix)
			info, err := os.Stat(candidate)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
				return "", errors.New("PATH entry is not a usable regular executable")
			}
			if !filepath.IsAbs(candidate) {
				return "", exec.ErrDot
			}
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

type productAuthorityError struct{ cause error }

func (e productAuthorityError) Error() string { return e.cause.Error() }
func (e productAuthorityError) Unwrap() error { return e.cause }
func setupProductErrorCode(err error) int {
	var authority productAuthorityError
	if errors.As(err, &authority) {
		return 2
	}
	return 1
}
