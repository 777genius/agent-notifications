package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

const portableSetupHelp = `Usage: claude-notifications portable-install|portable-remove [OPTIONS]
Explicit portable setup around pinned UAP. Does not guess HOME, cwd, or clientInfo.
Required:
  --integration codex|claude
  --package PATH            Portable package root (plugin.json + mcp.json)
  --control-root PATH       Existing managed control directory
  --runtime-root PATH       Existing managed runtime
  --global-config PATH      Canonical hook config
  --scope-root PATH         Explicit client scope root
  --client-config PATH      Selected client config root
  --client-executable PATH  Selected client executable (never launched from PATH)
  --uap-state PATH          UAP state-v2.json
  --uap-lock PATH           UAP mutation lock
  --uap-operations PATH     UAP directory-transaction journal
  --plugin-data PATH        UAP PLUGIN_DATA base
  --managed-root PATH       UAP managed projection root
  --installation-id ID      Shared UAP installation identity
  --expected-generation N   Current existing-installer generation
  --primary NAME            Ledger primary basename
  --helper PATH             Managed stdio helper bytes (default: runtime primary)
Optional:
  --mcp-config PATH         Owned global MCP: retire before install; restore after remove only once the locator is gone
  --operation-id ID         Explicit UAP operation identity
`

func agentPortableSetupMain(command string, args []string) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	help := flags.Bool("help", false, "")
	integration := flags.String("integration", "", "")
	pkg := flags.String("package", "", "")
	control := flags.String("control-root", "", "")
	runtimeRoot := flags.String("runtime-root", "", "")
	global := flags.String("global-config", "", "")
	scope := flags.String("scope-root", "", "")
	clientConfig := flags.String("client-config", "", "")
	clientExec := flags.String("client-executable", "", "")
	state := flags.String("uap-state", "", "")
	lock := flags.String("uap-lock", "", "")
	ops := flags.String("uap-operations", "", "")
	data := flags.String("plugin-data", "", "")
	managed := flags.String("managed-root", "", "")
	installID := flags.String("installation-id", "", "")
	generation := flags.String("expected-generation", "", "")
	primary := flags.String("primary", "", "")
	helper := flags.String("helper", "", "")
	mcp := flags.String("mcp-config", "", "")
	operation := flags.String("operation-id", "", "")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *help {
		fmt.Fprint(os.Stderr, portableSetupHelp)
		return 0
	}
	if command != "portable-install" && command != "portable-remove" {
		return 2
	}
	gen, err := strconv.ParseUint(*generation, 10, 64)
	if err != nil || gen == 0 {
		fmt.Fprintln(os.Stderr, "expected-generation must be a positive integer")
		return 2
	}
	var kind portable.Integration
	switch *integration {
	case "codex":
		kind = portable.Codex
	case "claude":
		kind = portable.Claude
	default:
		fmt.Fprintln(os.Stderr, "integration must be codex or claude")
		return 2
	}
	helperPath := *helper
	if helperPath == "" {
		helperPath = filepath.Join(*runtimeRoot, *primary)
	}
	mat, err := portablesetup.NewMaterializer(portablesetup.UAPRoots{
		StateFile: *state, LockFile: *lock, OperationsDir: *ops, PluginDataBase: *data,
		ManagedRoot: *managed, HelperExecutable: helperPath,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	snap, err := installruntime.ReadInstalledSnapshot(*control)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	req := portablesetup.MaterializeRequest{
		Identity: portablesetup.Identity{
			InstallationID: *installID, ComponentID: snap.Ledger.ID, Owner: "existing-installer",
			ScopeRoot: *scope, ControlRoot: *control, GlobalConfig: *global,
			RuntimeRoot: *runtimeRoot, Primary: *primary,
		},
		Integration: kind, ExpectedGeneration: gen, PackageRoot: *pkg,
		ClientConfigRoot: *clientConfig, ClientExecutable: *clientExec,
		Discovery:   portablesetup.Discovery{ConfigPath: *mcp, Command: filepath.Join(*runtimeRoot, *primary)},
		OperationID: *operation, HelperExecutable: helperPath,
	}
	if req.OperationID == "" {
		req.OperationID = command + "-" + string(kind)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if command == "portable-remove" {
		if err := mat.Remove(ctx, req); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	binding, err := mat.Install(ctx, req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	name, err := binding.Filename()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "portable binding published: %s\n", name)
	return 0
}
