package piauthority

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
)

// EnlistRuntimeRefresh keeps retained Pi discovery in the executable writer's
// existing transaction. It never changes package receipts, channel consent or
// observer authority. The old revision must still be independently provable.
func EnlistRuntimeRefresh(ctx context.Context, req *installruntime.Request) error {
	if req.RemoveConsumer || len(req.Files) == 0 {
		return nil
	}
	root := req.ControlRoot
	var err error
	if root == "" {
		root, err = installruntime.ControlRoot()
		if err != nil {
			return err
		}
	}
	root, err = installruntime.CanonicalPath(root)
	if err != nil {
		return err
	}
	ledger, _, err := installruntime.ReadOwnership(root)
	if err != nil {
		return err
	}
	type refresh struct {
		binding portable.Binding
		sha     string
		primary string
		missing string
	}
	var retained []refresh
	for key, consumer := range ledger.Consumers {
		if !strings.HasPrefix(key, "portable:") {
			continue
		}
		var b portable.Binding
		if json.Unmarshal([]byte(consumer.Registration), &b) != nil || b.Integration != portable.Pi {
			continue
		}
		for _, file := range req.Files {
			if file.Path != filepath.Join(b.RuntimeRoot, filepath.FromSlash(b.Primary)) {
				continue
			}
			wantKey, wantConsumer, _, e := b.Registration()
			if e != nil || wantKey != key || !reflect.DeepEqual(wantConsumer, consumer) || b.ControlRoot != root || !portable.ExactCommittedBinding(ledger, b) || file.Remove || file.Link != "" {
				return ErrDenied
			}
			digest := sha256.Sum256(file.Data)
			item := refresh{binding: b, sha: hex.EncodeToString(digest[:]), primary: file.Path}
			if file.Before == (installruntime.Identity{}) {
				item.missing = file.Path
			}
			retained = append(retained, item)
			req.ConfigPaths = append(req.ConfigPaths, filepath.Join(b.DataRoot, RuntimeFile))
		}
	}
	if len(retained) == 0 {
		return nil
	}
	// Fence the set of retained consumers as well as the writer's own observation.
	if req.ExpectedGeneration == nil {
		generation := ledger.Generation
		req.ExpectedGeneration = &generation
	}
	prepare := req.Prepare
	req.Prepare = func() ([]installruntime.File, error) {
		currentLedger, recovery, err := installruntime.ReadOwnership(root)
		if err != nil {
			return nil, err
		}
		if recovery || !reflect.DeepEqual(currentLedger, ledger) {
			return nil, ErrDenied
		}
		// Commit audits every managed file after Prepare, allowing its staged
		// missing siblings to be repaired. Pi qualification below independently
		// requires the old owned primary and the complete UAP/data authority.
		current := installruntime.InstalledSnapshot{Ledger: currentLedger, Enabled: currentLedger.Enabled}
		var files []installruntime.File
		if prepare != nil {
			files, err = prepare()
			if err != nil {
				return nil, err
			}
		}
		for _, item := range retained {
			b := item.binding
			revision, _, err := installedRevisionWithPrimaryRepair(ctx, b, stateFile(b), current, false, item.missing)
			if err != nil {
				return nil, fmt.Errorf("retained Pi binding %s blocks this runtime update; repair or remove the Pi integration first: %w", b.BindingID, err)
			}
			name, err := b.Filename()
			if err != nil {
				return nil, err
			}
			read, err := portable.ReadPiBinding(filepath.Join(b.DataRoot, name))
			if err != nil || read != b {
				return nil, ErrDenied
			}
			wanted := Runtime{SchemaVersion: 1, Locator: name, Executable: item.primary, PrimarySHA256: revision.PrimarySHA256, LoadedRevision: revision}
			old, err := json.Marshal(wanted)
			if err != nil {
				return nil, err
			}
			path := filepath.Join(b.DataRoot, RuntimeFile)
			before, err := installruntime.Fingerprint(path)
			if err != nil || before.Link != "" {
				return nil, ErrDenied
			}
			file, err := nativeconfig.New().ReadExactFile(path)
			if err != nil || file.Exists != before.Exists || file.Exists && !bytes.Equal(file.Body, old) {
				return nil, ErrDenied
			}
			wanted.PrimarySHA256, wanted.LoadedRevision.PrimarySHA256 = item.sha, item.sha
			body, err := json.Marshal(wanted)
			if err != nil {
				return nil, err
			}
			files = append(files, installruntime.File{Path: path, Before: before, Data: body, Mode: 0600})
		}
		return files, ctx.Err()
	}
	return nil
}
