// Command uapinstaller-sample is the external consumer of the published
// single-client installer API.
//
// Default invocation only constructs the public Engine to prove the package
// imports without a workspace, replace directive, or raw Store/Kernel types.
// Passing explicit roots runs install → inspect → no-op repeat → repair → remove.
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
	config := flag.String("config", "", "absolute client config root")
	helper := flag.String("helper", "", "absolute managed helper executable")
	client := flag.String("client-exe", "", "absolute client executable")
	flag.Parse()
	if *state == "" && *pkg == "" {
		if _, err := uapinstaller.New(uapinstaller.Config{StateRoot: "/uapinstaller-sample-state"}); err != nil {
			fmt.Fprintf(os.Stderr, "new: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("external import ok")
		return
	}
	if err := runDemo(*state, *pkg, *config, *helper, *client); err != nil {
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
	req := uapinstaller.Request{
		Operation: uapinstaller.OpInstall, PackageRoot: pkg, ClientID: "codex",
		ClientConfigRoot: config, ClientExecutable: client,
		InstallationID: "00000000-0000-4000-8000-000000000099",
		OperationID:    "sample-install", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		return err
	}
	defer func() { _ = prepared.Close() }()
	fmt.Printf("source-digest=%s algorithm=%s\n", prepared.Plan().TreeDigest, prepared.Plan().DigestAlgorithm)
	installed, err := eng.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		return err
	}
	fmt.Printf("install=%s\n", installed.Outcome)
	view, err := eng.Inspect(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("installations=%d recovery=%t\n", len(view.Installations), view.Recovery.Required)
	again, err := eng.Prepare(ctx, req)
	if err != nil {
		return err
	}
	defer func() { _ = again.Close() }()
	repeat, err := eng.Apply(ctx, again, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		return err
	}
	fmt.Printf("repeat=%s no-change=%t\n", repeat.Outcome, repeat.NoChange)
	repaired, err := eng.Prepare(ctx, uapinstaller.Request{
		Operation: uapinstaller.OpRepair, PackageRoot: pkg, ClientID: "codex",
		ClientConfigRoot: config, ClientExecutable: client, InstallationID: req.InstallationID,
		OperationID: "sample-repair", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		return err
	}
	defer func() { _ = repaired.Close() }()
	repair, err := eng.Apply(ctx, repaired, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		return err
	}
	fmt.Printf("repair=%s\n", repair.Outcome)
	rm, err := eng.Prepare(ctx, uapinstaller.Request{
		Operation: uapinstaller.OpRemove, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: client, InstallationID: req.InstallationID,
		OperationID: "sample-remove", ExternalUninstalled: true,
	})
	if err != nil {
		return err
	}
	defer func() { _ = rm.Close() }()
	removed, err := eng.Apply(ctx, rm, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		return err
	}
	fmt.Printf("remove=%s\n", removed.Outcome)
	return nil
}
