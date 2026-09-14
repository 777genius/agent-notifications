package uapinstaller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/dirswap"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/loader"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/processlock"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/specregistry"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/managedstdio"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/planner"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/transaction"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/usecase"
)

// Engine is the process-local installer. It does not export Store or Kernel.
type Engine struct {
	cfg   Config
	store statev2.Store
	// persistObservations enables the §5.4 PersistAuthoritativeObservations
	// seam. Prepare still uses DryRun and does not persist.
	persistObservations bool
}

// New validates Config and copies it. It does not create directories, open a
// journal, or execute a helper or client.
func New(cfg Config) (*Engine, error) {
	resolved, err := cfg.resolved()
	if err != nil {
		return nil, err
	}
	return &Engine{cfg: resolved, store: statev2.Store{Path: resolved.StateFile}}, nil
}

func (e *Engine) lifecycle(helper *managedstdio.Source, facts BindingFacts) usecase.Service {
	plan := planner.Planner{ManagedRoot: e.cfg.ManagedRoot}
	stager := seamStager{
		Stager:      providers.Stager{LauncherSource: helper},
		serverName:  e.cfg.ServerName,
		projectArgs: e.cfg.ProjectArgs,
		facts:       facts,
	}
	inner := providers.Activator{Runner: e.cfg.Runner}
	return usecase.Service{
		StateStore: e.store, Planner: plan, Targets: plan, Stager: stager,
		Activator:  seamActivator{inner: inner, onCommitted: e.cfg.OnCommittedBinding, store: e.store, facts: facts},
		PluginData: providers.PluginDataManager{Base: e.cfg.PluginDataBase},
		Lock:       processlock.Lock{Path: e.cfg.LockFile},
		Kernel:     transaction.Kernel{StateStore: e.store, Directory: dirswap.Manager{JournalDir: e.cfg.OperationsDir}},
		// NativeObserver stays unset unless a future host injects a live client
		// identity check. The default observer queries the selected executable as
		// Claude/Codex and blocks mutation when that identity is indeterminate.
	}
}

func (e *Engine) report(phase ProgressPhase) {
	if e.cfg.Progress == nil {
		return
	}
	e.cfg.Progress(ProgressEvent{Phase: phase})
}

func (e *Engine) helper() (*managedstdio.Source, error) {
	if e.cfg.HelperExecutable == "" {
		return nil, fmt.Errorf("%w: HelperExecutable is required for this operation", ErrInvalidConfig)
	}
	return managedstdio.NewSource(e.cfg.HelperExecutable, e.cfg.HelperVersion)
}

// helperIdentity is the version plus SHA-256 of the helper bytes. UAP
// managedstdio.Source stores the same digest; Prepare reports it without
// requiring execute bits that Go Windows FileMode omits on regular files.
func (e *Engine) helperIdentity() (version, digest string) {
	version = e.cfg.HelperVersion
	if e.cfg.HelperExecutable == "" {
		return version, ""
	}
	body, err := os.ReadFile(e.cfg.HelperExecutable)
	if err != nil {
		return version, ""
	}
	sum := sha256.Sum256(body)
	return version, hex.EncodeToString(sum[:])
}

func (e *Engine) ensureDirs() error {
	for _, dir := range []string{
		filepath.Dir(e.cfg.StateFile), filepath.Dir(e.cfg.LockFile),
		e.cfg.OperationsDir, e.cfg.PluginDataBase, e.cfg.ManagedRoot, e.cfg.TempRoot,
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

func newLoader() (loader.Loader, error) {
	reg, err := specregistry.New()
	if err != nil {
		return loader.Loader{}, err
	}
	return loader.Loader{Registry: reg}, nil
}
