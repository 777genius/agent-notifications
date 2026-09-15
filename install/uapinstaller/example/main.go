// Command uapinstaller-sample is the external consumer of the published
// installer API.
//
// Default invocation only constructs the public Engine to prove the package
// imports without a workspace, replace directive, or raw Store/Kernel types.
// Passing explicit roots runs install → inspect → recover → no-op repeat → update → repair → remove.
// Passing -claude-config as well uses Request.Targets for Claude+Codex together.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/777genius/agent-notifications/install/uapinstaller"
)

func main() {
	state := flag.String("state", "", "absolute UAP state root")
	pkg := flag.String("package", "", "absolute local package root")
	config := flag.String("config", "", "absolute Codex client config root")
	claudeConfig := flag.String("claude-config", "", "absolute Claude client config root; enables group Request.Targets")
	helper := flag.String("helper", "", "absolute managed helper executable")
	client := flag.String("client-exe", "", "absolute Codex client executable")
	claudeExe := flag.String("claude-exe", "", "absolute Claude client executable; defaults to -client-exe")
	flag.Parse()
	if *state == "" && *pkg == "" {
		if _, err := uapinstaller.New(uapinstaller.Config{StateRoot: "/uapinstaller-sample-state"}); err != nil {
			fmt.Fprintf(os.Stderr, "new: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("external import ok")
		return
	}
	claudeBin := *claudeExe
	if claudeBin == "" {
		claudeBin = *client
	}
	var err error
	if *claudeConfig != "" {
		err = runGroupDemo(*state, *pkg, *config, *claudeConfig, *helper, *client, claudeBin)
	} else {
		err = runDemo(*state, *pkg, *config, *helper, *client)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sample: %v\n", err)
		os.Exit(1)
	}
}

func runDemo(state, pkg, config, helper, client string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: state, HelperExecutable: helper})
	if err != nil {
		return err
	}
	printDiscover(eng)
	req := uapinstaller.Request{
		Operation: uapinstaller.OpInstall, PackageRoot: pkg, ClientID: "codex",
		ClientConfigRoot: config, ClientExecutable: client,
		InstallationID: "00000000-0000-4000-8000-000000000099",
		OperationID:    "sample-install", RequiredComponents: []string{"mcp", "skills"},
	}
	if err := runLifecycle(ctx, eng, req, uapinstaller.Request{
		Operation: uapinstaller.OpUpdate, PackageRoot: pkg, ClientID: "codex",
		ClientConfigRoot: config, ClientExecutable: client, InstallationID: req.InstallationID,
		OperationID: "sample-update", RequiredComponents: []string{"mcp", "skills"},
	}, uapinstaller.Request{
		Operation: uapinstaller.OpRepair, PackageRoot: pkg, ClientID: "codex",
		ClientConfigRoot: config, ClientExecutable: client, InstallationID: req.InstallationID,
		OperationID: "sample-repair", RequiredComponents: []string{"mcp", "skills"},
	}, uapinstaller.Request{
		Operation: uapinstaller.OpRemove, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: client, InstallationID: req.InstallationID,
		OperationID: "sample-remove", ExternalUninstalled: true,
	}); err != nil {
		return err
	}
	return nil
}

func runGroupDemo(state, pkg, config, claudeConfig, helper, client, claudeExe string) error {
	if config == "" {
		return fmt.Errorf("-config is required with -claude-config")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: state, HelperExecutable: helper})
	if err != nil {
		return err
	}
	printDiscover(eng)
	targets := []uapinstaller.ClientTarget{
		{ClientID: "codex", ClientConfigRoot: config, ClientExecutable: client},
		{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: claudeExe},
	}
	req := uapinstaller.Request{
		Operation: uapinstaller.OpInstall, PackageRoot: pkg, InstallationID: "00000000-0000-4000-8000-000000000099",
		OperationID: "sample-group-install", RequiredComponents: []string{"mcp", "skills"},
		ClientExecutable: client, Targets: targets,
	}
	removeTargets := []uapinstaller.ClientTarget{
		{ClientID: "codex", ClientConfigRoot: config, ClientExecutable: client, ExternalUninstalled: true},
		{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: claudeExe},
	}
	return runLifecycle(ctx, eng, req, uapinstaller.Request{
		Operation: uapinstaller.OpUpdate, PackageRoot: pkg, InstallationID: req.InstallationID,
		OperationID: "sample-group-update", RequiredComponents: []string{"mcp", "skills"},
		ClientExecutable: client, Targets: targets,
	}, uapinstaller.Request{
		Operation: uapinstaller.OpRepair, PackageRoot: pkg, InstallationID: req.InstallationID,
		OperationID: "sample-group-repair", RequiredComponents: []string{"mcp", "skills"},
		ClientExecutable: client, Targets: targets,
	}, uapinstaller.Request{
		Operation: uapinstaller.OpRemove, InstallationID: req.InstallationID,
		OperationID: "sample-group-remove", ClientExecutable: client, Targets: removeTargets,
	})
}

func runLifecycle(ctx context.Context, eng *uapinstaller.Engine, install, update, repair, remove uapinstaller.Request) error {
	prepared, err := eng.Prepare(ctx, install)
	if err != nil {
		return err
	}
	defer func() { _ = prepared.Close() }()
	fmt.Printf("source-digest=%s algorithm=%s targets=%d\n", prepared.Plan().TreeDigest, prepared.Plan().DigestAlgorithm, len(prepared.Plan().Targets))
	installed, err := eng.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		return err
	}
	fmt.Printf("install=%s result-targets=%d\n", installed.Outcome, len(installed.Targets))
	view, err := eng.Inspect(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("installations=%d recovery=%t\n", len(view.Installations), view.Recovery.Required)
	recovered, err := eng.Recover(ctx, view)
	if err != nil {
		return err
	}
	fmt.Printf("recover=%s\n", recovered.Outcome)
	repeat, err := applyConfirmed(ctx, eng, install)
	if err != nil {
		return err
	}
	fmt.Printf("repeat=%s no-change=%t result-targets=%d\n", repeat.Outcome, repeat.NoChange, len(repeat.Targets))
	got, err := applyConfirmed(ctx, eng, update)
	if err != nil {
		return err
	}
	fmt.Printf("update=%s no-change=%t\n", got.Outcome, got.NoChange)
	got, err = applyConfirmed(ctx, eng, repair)
	if err != nil {
		return err
	}
	fmt.Printf("repair=%s\n", got.Outcome)
	got, err = applyConfirmed(ctx, eng, remove)
	if err != nil {
		return err
	}
	fmt.Printf("remove=%s\n", got.Outcome)
	return nil
}

func applyConfirmed(ctx context.Context, eng *uapinstaller.Engine, req uapinstaller.Request) (uapinstaller.Result, error) {
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		return uapinstaller.Result{}, err
	}
	defer func() { _ = prepared.Close() }()
	return eng.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
}

func printDiscover(eng *uapinstaller.Engine) {
	got := eng.Discover()
	fmt.Printf("discover=%d", len(got))
	for _, client := range got {
		fmt.Printf(" %s", client.ClientID)
	}
	fmt.Println()
}
