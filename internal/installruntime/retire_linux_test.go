package installruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Exercise production transactions on disposable Linux directories using unique
// published generation paths. This does not qualify Darwin lsof or LaunchServices.
func TestRetirementTransitionAndRecovery(t *testing.T) {
	oldDrain := nativeDrainCheck
	defer func() { nativeDrainCheck = oldDrain }()
	for _, tc := range []struct{ crash, legacy bool }{{}, {crash: true}, {legacy: true}, {crash: true, legacy: true}} {
		t.Run(fmt.Sprintf("crash=%v/legacy=%v", tc.crash, tc.legacy), func(t *testing.T) {
			ctx, r := request(t)
			r.Consumer.Commands = []string{"existing hook --literal"}
			stage := func(label string) *NativeChange {
				source := nativeFixture(t)
				if err := os.WriteFile(filepath.Join(source, "version"), []byte(label), 0600); err != nil {
					t.Fatal(err)
				}
				c, err := StageNative(ctx, r.ControlRoot, source)
				if err != nil {
					t.Fatal(err)
				}
				c.After.DecoderFloor = 1
				return c
			}
			r.Native = stage("A")
			a, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			pathA := a.Native.Path
			hashA := a.Native.SHA256
			r.Native = stage("B")
			if tc.legacy {
				// Model an older allocator before commit, never migrate a retained record.
				legacy := strings.TrimSuffix(r.Native.Staged, ".app")
				if err := os.Rename(r.Native.Staged, legacy); err != nil {
					t.Fatal(err)
				}
				r.Native.Staged = legacy
			}
			candidate := r.Native
			if tc.crash {
				r.Fault = func(phase string) error {
					if phase == "native" {
						return fmt.Errorf("crash after promote")
					}
					return nil
				}
				if _, err := Commit(ctx, r); err == nil {
					t.Fatal("missing promotion crash")
				}
				if got, err := treeFingerprint(pathA); err != nil || got != hashA {
					t.Fatalf("generation A mutated during crashed B promote: %s %v", got, err)
				}
				if err := DiscardNative(ctx, r.ControlRoot, candidate); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(pathA); err != nil {
					t.Fatal("generation A lost after discard")
				}
				r.Fault, r.Native = nil, nil
			}
			b, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if b.Native.Path == pathA {
				t.Fatal("B reused live callback identity")
			}
			if b.Native.PreviousPath != pathA || b.Native.PreviousSHA256 != hashA || b.Native.PreviousDirectoryID != a.Native.DirectoryID {
				t.Fatal("predecessor record lost exact path/hash/inode")
			}
			if _, err := os.Stat(pathA); err != nil {
				t.Fatal("generation A disappeared after B")
			}
			if _, err := os.Stat(b.Native.Path); err != nil {
				t.Fatal("generation B missing")
			}
			if err := DiscardNative(ctx, r.ControlRoot, candidate); err != nil {
				t.Fatal(err)
			}
			r.Native = nil
			if again, err := Commit(ctx, r); err != nil || !reflect.DeepEqual(again.Native, b.Native) {
				t.Fatalf("repeat recovery changed native record: %v", err)
			} else {
				b = again
			}
			called := 0
			nativeDrainCheck = func(context.Context, string) error {
				called++
				return fmt.Errorf("drain should not run for published generations")
			}
			r.RetireNative = true
			r.ExpectedGeneration = &b.Generation
			retired, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if called != 0 {
				t.Fatal("published predecessor was drained")
			}
			if !reflect.DeepEqual(retired.Consumers, b.Consumers) || retired.Enabled != b.Enabled {
				t.Fatal("retirement mutated consumer registration or eligibility")
			}
			if retired.Native.Path != b.Native.Path || retired.Native.PreviousPath != pathA {
				t.Fatal("retire mutated published generation inventory")
			}
			if _, err := os.Stat(pathA); err != nil {
				t.Fatal("retire deleted published generation A")
			}
			r.RetireNative = false
			r.ExpectedGeneration = nil
			c := stage("C")
			r.Native = c
			final, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if final.Native.Path == pathA || final.Native.Path == b.Native.Path {
				t.Fatal("C reused a published callback identity")
			}
			if len(final.Native.Published) != 3 {
				t.Fatalf("published inventory: %d", len(final.Native.Published))
			}
			for _, p := range []string{pathA, b.Native.Path, final.Native.Path} {
				if _, err := os.Stat(p); err != nil {
					t.Fatalf("published generation missing: %s %v", p, err)
				}
			}
		})
	}
}
