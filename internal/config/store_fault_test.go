package config

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
)

type brokenOutput struct {
	writeErr, syncErr, closeErr error
	short, closed               bool
}

func (f *brokenOutput) Write(b []byte) (int, error) {
	if f.short {
		return len(b) - 1, f.writeErr
	}
	return len(b), f.writeErr
}
func (f *brokenOutput) Sync() error  { return f.syncErr }
func (f *brokenOutput) Close() error { f.closed = true; return f.closeErr }
func TestArtifactIOFailures(t *testing.T) {
	injected := errors.New("injected")
	for _, f := range []*brokenOutput{{writeErr: syscall.ENOSPC}, {writeErr: injected}, {short: true}, {syncErr: injected}, {closeErr: injected}} {
		err := finishArtifact(f, []byte("whole document"))
		if err == nil || !f.closed {
			t.Fatal("failure swallowed or handle leaked")
		}
		if f.short && !errors.Is(err, io.ErrShortWrite) {
			t.Fatal(err)
		}
	}
}

type faultMutationFiles struct {
	mutationFiles
	artifactKind      string
	publishErr        error
	syncAt, syncCalls int
}

func (f *faultMutationFiles) writeArtifact(base, kind string, data []byte) (string, error) {
	if f.artifactKind == kind {
		return "", io.ErrShortWrite
	}
	return f.mutationFiles.writeArtifact(base, kind, data)
}
func (f *faultMutationFiles) publish(ctx context.Context, temp, name string, exists bool, a, b []byte) error {
	if f.publishErr != nil {
		return f.publishErr
	}
	return f.mutationFiles.publish(ctx, temp, name, exists, a, b)
}
func (f *faultMutationFiles) sync() error {
	f.syncCalls++
	if f.syncCalls == f.syncAt {
		return io.ErrClosedPipe
	}
	return f.mutationFiles.sync()
}
func TestTransactionFailureClassification(t *testing.T) {
	for _, phase := range []string{"backup", "tmp", "backup-sync", "publish", "parent-sync"} {
		t.Run(phase, func(t *testing.T) {
			env := storeEnv(t)
			r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
			if e != nil {
				t.Fatal(e)
			}
			m, e := beginMutation(context.Background(), env)
			if e != nil {
				t.Fatal(e)
			}
			defer m.close()
			old, e := currentDocument(m, AssetContext{})
			if e != nil {
				t.Fatal(e)
			}
			next, e := ParseDocument([]byte(`{"debug":{"benchmark":true}}`), m.selection.Path, true)
			if e != nil {
				t.Fatal(e)
			}
			faults := &faultMutationFiles{mutationFiles: m.parent}
			switch phase {
			case "backup", "tmp":
				faults.artifactKind = phase
			case "backup-sync":
				faults.syncAt = 1
			case "publish":
				faults.publishErr = os.ErrPermission
			case "parent-sync":
				faults.syncAt = 2
			}
			m.parent = faults
			result, e := commitDocument(context.Background(), env, m, old, next, Result{Selection: r.Selection, Revision: r.Revision})
			if e == nil {
				t.Fatal("failure swallowed")
			}
			got, readErr := os.ReadFile(r.Selection.Path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if phase == "parent-sync" {
				var ce *Error
				if !errors.As(e, &ce) || ce.Code != ConfigCommitUncertain || !result.Changed || !bytes.Equal(got, next.Bytes()) {
					t.Fatalf("uncertain classification %v %+v", e, result)
				}
			} else if result.Changed || !bytes.Equal(got, old.Bytes()) {
				t.Fatal("definite failure changed original")
			}
		})
	}
}
