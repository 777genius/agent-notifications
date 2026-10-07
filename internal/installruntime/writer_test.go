package installruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedOldWriterRefusedWithoutExecutionOrMutation(t *testing.T) {
	ctx, r := request(t)
	path := filepath.Join(r.RuntimeRoot, "claude-notifications-linux-amd64")
	compatible := []byte("inert " + WriterProtocolMarker)
	r.Files = []File{{Path: path, Data: compatible, Mode: 0755}}
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(r.ControlRoot, "ownership.json"))
	spy := filepath.Join(t.TempDir(), "EXECUTED")
	r.Files = []File{{Path: path, Before: l.Files[path], Data: []byte("#!/bin/sh\ntouch '" + spy + "'\n"), Mode: 0755}}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("old writer promoted")
	}
	after, _ := os.ReadFile(filepath.Join(r.ControlRoot, "ownership.json"))
	actual, _ := os.ReadFile(path)
	if string(before) != string(after) || string(actual) != string(compatible) {
		t.Fatal("rejected writer mutated installed state")
	}
	if _, err := os.Stat(spy); !os.IsNotExist(err) {
		t.Fatal("old writer executed")
	}
}

func TestRollbackCannotRestoreHistoricalWriter(t *testing.T) {
	ctx, r := request(t)
	path := filepath.Join(r.RuntimeRoot, "claude-notifications-linux-amd64")
	if err := os.MkdirAll(r.RuntimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("historical destructive writer"), 0755); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Files = []File{{Path: path, Before: before, Data: []byte(WriterProtocolMarker), Mode: 0755}}
	r.Fault = func(phase string) error {
		if phase == "promotion:"+path {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("fault not reached")
	}
	r.Fault = nil
	r.Files = nil
	r.RollbackPending = true
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("rollback restored incompatible writer")
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != WriterProtocolMarker {
		t.Fatal("compatible kernel replaced")
	}
}

func TestGenericWriterFloorAndAliases(t *testing.T) {
	for _, name := range []string{"claude-notifications", "claude-notifications.exe"} {
		t.Run(name, func(t *testing.T) {
			ctx, r := request(t)
			path := filepath.Join(r.RuntimeRoot, name)
			r.Files = []File{{Path: path, Data: []byte(WriterProtocolMarker), Mode: 0755}}
			l, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			r.Files = []File{{Path: path, Before: l.Files[path], Data: []byte("historical"), Mode: 0755}}
			if _, err = Commit(ctx, r); err == nil {
				t.Fatal("generic historical writer accepted")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != WriterProtocolMarker {
				t.Fatal("rejected update changed writer", err)
			}
		})
	}
	root := t.TempDir()
	alias := File{Path: filepath.Join(root, "claude-notifications"), Link: "claude-notifications-linux-amd64"}
	target := File{Path: filepath.Join(root, alias.Link), Data: []byte(WriterProtocolMarker), Mode: 0755}
	if err := validateWriterFiles([]File{alias, target}); err != nil {
		t.Fatal(err)
	}
	for _, files := range [][]File{{alias}, {alias, {Path: target.Path, Data: []byte("old")}}, {alias, {Path: target.Path, Link: "foreign"}}, {alias, {Path: target.Path, Data: target.Data, Remove: true}}} {
		if err := validateWriterFiles(files); err == nil {
			t.Fatal("unvalidated alias accepted")
		}
	}
	alias.Link = "unmanaged"
	target.Path = filepath.Join(root, "unmanaged")
	if err := validateWriterFiles([]File{alias, target}); err == nil {
		t.Fatal("unmanaged alias target accepted")
	}
}
