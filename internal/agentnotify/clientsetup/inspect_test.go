//go:build linux || darwin

package clientsetup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func inspectionTree(t *testing.T, root string) map[string]installruntime.Identity {
	t.Helper()
	out := map[string]installruntime.Identity{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		id, err := installruntime.Fingerprint(p)
		out[p] = id
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestInspectSharedCalculation(t *testing.T) {
	for _, provider := range []registration.Provider{registration.Codex, registration.Claude} {
		t.Run(string(provider), func(t *testing.T) {
			f := fresh(t, provider)
			before := inspectionTree(t, filepath.Dir(f.r.ControlRoot))
			facts, err := Inspect(f.ctx, f.r)
			if err != nil || facts.Registered || facts.SkillProjected {
				t.Fatal(facts, err)
			}
			if !reflect.DeepEqual(before, inspectionTree(t, filepath.Dir(f.r.ControlRoot))) {
				t.Fatal("inspection wrote files")
			}
			f.apply(t)
			before = inspectionTree(t, filepath.Dir(f.r.ControlRoot))
			facts, err = Inspect(f.ctx, f.r)
			if err != nil || !facts.Registered || facts.SkillProjected {
				t.Fatal(facts, err)
			}
			if !reflect.DeepEqual(before, inspectionTree(t, filepath.Dir(f.r.ControlRoot))) {
				t.Fatal("inspection changed installed state")
			}
			put(t, statePath(f.r), []byte(`{"Schema":999}`))
			_, inspectErr := Inspect(f.ctx, f.r)
			_, applyErr := Apply(f.ctx, f.r)
			if inspectErr == nil || applyErr == nil || inspectErr.Error() != applyErr.Error() {
				t.Fatal(inspectErr, applyErr)
			}
		})
	}
}
func TestInspectCancellation(t *testing.T) {
	f := fresh(t, registration.Codex)
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	before := inspectionTree(t, filepath.Dir(f.r.ControlRoot))
	if _, err := Inspect(ctx, f.r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, inspectionTree(t, filepath.Dir(f.r.ControlRoot))) {
		t.Fatal("canceled inspection wrote")
	}
}
