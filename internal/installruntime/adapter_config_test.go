package installruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAdapterDocumentUsesSeparateLockAndSurvivesFinalRemoval(t *testing.T) {
	ctx, r := request(t)
	native, err := CanonicalPath(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	coordination := native + ".agent-notifications-qoder"
	// Qoder's real lock is a directory. The installer must never open it as a
	// regular flock file or retain the external document in the asset ledger.
	if err = os.Mkdir(native+".lock", 0700); err != nil {
		t.Fatal(err)
	}
	r.ConfigPaths = []string{coordination}
	r.AdapterConfigPaths = []string{native}
	asset := filepath.Join(r.RuntimeRoot, "managed-hook")
	r.Files = []File{{Path: asset, Data: []byte("owned"), Mode: 0700}, {Path: native, Data: []byte(`{"enabled":true}`), Mode: 0600}}
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, owned := OwnedFile(l, native); owned {
		t.Fatal("external native document became an owned runtime asset")
	}
	info, err := os.Stat(native + ".lock")
	if err != nil || !info.IsDir() {
		t.Fatal("native lock directory was changed")
	}
	if info, err = os.Stat(coordination + ".lock"); err != nil || !info.Mode().IsRegular() {
		t.Fatal("separate installer lock was not created")
	}
	user := []byte(`{"enabled":false,"foreign":"preserved"}`)
	if err = os.WriteFile(native, user, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = Commit(ctx, Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: r.Owner, ConsumerID: r.ConsumerID, RemoveConsumer: true, ExpectedGeneration: &l.Generation, ConfigPaths: r.ConfigPaths, AdapterConfigPaths: r.AdapterConfigPaths})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(native)
	if err != nil || string(data) != string(user) {
		t.Fatal("final consumer removal changed native user settings")
	}
	if _, err = os.Lstat(asset); !os.IsNotExist(err) {
		t.Fatal("final consumer removal retained its owned runtime asset")
	}
}

func TestAdapterDocumentRefusesProtectedAssetsAndStaleCAS(t *testing.T) {
	for _, kind := range []string{"control", "runtime", "owned", "relative", "alias", "stale"} {
		t.Run(kind, func(t *testing.T) {
			ctx, r := request(t)
			owned, err := CanonicalPath(filepath.Join(t.TempDir(), "owned-elsewhere"))
			if err != nil {
				t.Fatal(err)
			}
			r.Files = []File{{Path: owned, Data: []byte("managed asset"), Mode: 0600}}
			l, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			native, err := CanonicalPath(filepath.Join(t.TempDir(), "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(native, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			path := native
			switch kind {
			case "control":
				path, err = CanonicalPath(filepath.Join(r.ControlRoot, "foreign.json"))
			case "runtime":
				path, err = CanonicalPath(filepath.Join(r.RuntimeRoot, "foreign.json"))
			case "owned":
				path = owned
			case "relative":
				path = "relative.json"
			case "alias":
				path = filepath.Dir(native) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(native)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := Fingerprint(path)
			if kind == "stale" {
				if err = os.WriteFile(native, []byte("user changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err = Commit(ctx, Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: r.Owner, ConsumerID: r.ConsumerID, RefreshOnly: true, ExpectedGeneration: &l.Generation, ConfigPaths: []string{native + ".agent-notifications-qoder"}, AdapterConfigPaths: []string{path}, Files: []File{{Path: path, Before: before, Data: []byte("replacement"), Mode: 0600}}})
			if err == nil {
				t.Fatalf("unsafe adapter mutation accepted: %s", kind)
			}
			data, _ := os.ReadFile(owned)
			if string(data) != "managed asset" {
				t.Fatal("protected managed asset changed")
			}
			if kind == "stale" {
				data, _ = os.ReadFile(native)
				if string(data) != "user changed" {
					t.Fatal("stale CAS overwrote native edit")
				}
			}
		})
	}
}

func TestAdapterDocumentRecoveryRetainsCoordinationAndOwnership(t *testing.T) {
	ctx, r := request(t)
	native, err := CanonicalPath(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	coordination := native + ".agent-notifications-qoder"
	r.ConfigPaths = []string{coordination}
	r.AdapterConfigPaths = []string{native}
	r.Files = []File{{Path: native, Data: []byte(`{"native":true}`), Mode: 0600}}
	r.Fault = func(phase string) error {
		if phase == "transaction" {
			return fmt.Errorf("TEST interrupted adapter publication")
		}
		return nil
	}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("fault was not reached")
	}
	l, err := Recover(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, owned := OwnedFile(l, native); owned {
		t.Fatal("recovery adopted external native settings")
	}
	if _, err = os.Lstat(native + ".lock"); !os.IsNotExist(err) {
		t.Fatal("recovery created a native host lock")
	}
	if _, err = os.Stat(coordination + ".lock"); err != nil {
		t.Fatal("recovery did not retain coordination lock")
	}
}
