//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/windows"
)

// Red on the old CLI: a real final-Commit refusal has no measured attribution.
// Changing refusal, repairing ACLs or failing before final Commit is also red.
func TestWindowsSetupGlobalConfigAdmissionDiagnostic(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign_mutation_%t", foreign), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			// The CLI requires exact physical spelling, including Windows long
			// names/case. Resolve the existing owned temp ancestor before deriving
			// the private TEST suffix, as the normal setup fixtures already do.
			physicalTemp, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(physicalTemp, "TEST-private")
			_ = setupAdmissionDirectory(t, root, false)
			parent := filepath.Join(root, "ordinary-global")
			held := setupAdmissionDirectory(t, parent, foreign)
			descriptor := setupAdmissionDescriptor(t, held)
			global := filepath.Join(parent, "ordinary.json")
			config := []byte(`{"notifications":{"desktop":{"enabled":true,"sound":false,"clickToFocus":false}}}`)
			sentinel := filepath.Join(parent, "sentinel")
			for p, data := range map[string][]byte{global: config, sentinel: []byte("TEST unchanged")} {
				if err := os.WriteFile(p, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			control, runtimeRoot := filepath.Join(root, "control"), filepath.Join(root, "runtime")
			hook := filepath.Join(runtimeRoot, "hook")
			ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot,
				Owner: "existing-installer", ConsumerID: "hooks", Files: []installruntime.File{{Path: hook, Data: []byte("TEST inert owned hook"), Mode: 0700}}})
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"enable", "--control-root", control, "--runtime-root", runtimeRoot, "--global-config", global, "--json",
				"--expected-generation", strconv.FormatUint(ledger.Generation, 10), "--navigation", "none", "--allow-unknown-caller", "false", "--allow-caller-asserted", "false"}
			composition := agentNotifySetupComposition{globalConfigPath: func() (string, error) { return global, nil }, setup: func(o *notifysetup.Options) {
				o.JournalClock = journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "TEST", Seconds: 100, Available: true} })
			}}
			var out bytes.Buffer
			code := agentNotifySetupExecute(ctx, args, &out, composition)
			var receipt struct {
				Reason     string `json:"reason"`
				Generation uint64 `json:"generation"`
				Diagnostic *struct {
					Phase          string `json:"phase"`
					Role           string `json:"role"`
					Classification string `json:"classification"`
					ACEType        uint8  `json:"ace_type"`
					ACEFlags       uint8  `json:"ace_flags"`
					Mask           uint32 `json:"access_mask"`
				} `json:"diagnostic"`
			}
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			if foreign {
				if code != 1 || receipt.Reason != "policy_commit_failed" || receipt.Generation != 3 {
					t.Fatalf("wrong boundary: code=%d %s", code, out.Bytes())
				}
				d := receipt.Diagnostic
				if d == nil || d.Phase != "config_lock_admission" || d.Role != "global_config" || d.Classification != "foreign_mutation_ace" ||
					d.ACEType != 0 || d.ACEFlags != 3 || d.Mask != 2 {
					t.Fatalf("missing/wrong measured diagnostic: %s", out.Bytes())
				}
				// Independently close the public DTO so paths, SIDs and raw error bodies
				// cannot accidentally become part of the externally visible failure.
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
					t.Fatal(err)
				}
				if len(fields) != 3 {
					t.Fatal("unexpected public failure fields")
				}
				rawDiagnostic := fields["diagnostic"]
				fields = nil
				if err := json.Unmarshal(rawDiagnostic, &fields); err != nil {
					t.Fatal(err)
				}
				if len(fields) != 6 {
					t.Fatal("unexpected public diagnostic fields")
				}
				for _, key := range []string{"phase", "role", "classification", "ace_type", "ace_flags", "access_mask"} {
					if _, ok := fields[key]; !ok {
						t.Fatal("missing closed diagnostic field")
					}
				}
				for _, p := range []string{global + ".lock", filepath.Join(control, "transaction.json")} {
					if _, err := os.Lstat(p); !os.IsNotExist(err) {
						t.Fatalf("admitted publication: %v", err)
					}
				}
			} else if code != 0 || receipt.Reason != "enabled" || receipt.Diagnostic != nil {
				t.Fatalf("private control failed: code=%d %s", code, out.Bytes())
			}
			policy, err := installruntime.ReadUserPolicy(control)
			if err != nil || policy.Enabled == foreign {
				t.Fatalf("policy intent changed incorrectly: %v", err)
			}
			observed, err := installruntime.ReadInstalledSnapshot(control)
			if err != nil || observed.Ledger.Enabled == foreign || observed.Ledger.Native != nil || len(observed.Ledger.WindowsRetained) != 0 {
				t.Fatalf("unexpected managed state: %v", err)
			}
			if !bytes.Equal(descriptor, setupAdmissionDescriptor(t, held)) {
				t.Fatal("global parent descriptor changed")
			}
			for p, data := range map[string][]byte{global: config, sentinel: []byte("TEST unchanged")} {
				actual, err := os.ReadFile(p)
				if err != nil || !bytes.Equal(actual, data) {
					t.Fatal("config/sentinel changed")
				}
			}
		})
	}
}

// Creation-time descriptors define an independent TEST profile, never repair
// a failed admitted object. The unsafe parent grant is exactly OI|CI, mask 2.
func setupAdmissionDirectory(t *testing.T, path string, foreign bool) windows.Handle {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := "O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	if foreign {
		sddl += "(A;OICI;0x00000002;;;WD)"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(name, &sa); err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Error(err)
		}
	})
	return h
}
func setupAdmissionDescriptor(t *testing.T, h windows.Handle) []byte {
	t.Helper()
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if sd.Length() == 0 || sd.Length() > 65536 {
		t.Fatal("unbounded descriptor")
	}
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(sd)), int(sd.Length()))...)
}
