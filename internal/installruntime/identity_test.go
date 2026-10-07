package installruntime

import (
	"runtime"
	"testing"
)

// Pure logic evidence; actual Darwin filesystem/recovery tests live in the
// Darwin-tagged suite and must execute on ephemeral macOS CI.
func TestPersistedIdentityComparatorLogic(t *testing.T) {
	for _, tc := range []struct {
		name, stored, current, parent string
		want                          bool
	}{
		{"equal", "1:10", "1:10", "2", true},
		{"renumber", "1:10", "2:10", "2", true},
		{"boundary", "1:10", "2:10", "3", false},
		{"replacement", "1:10", "2:11", "2", false},
		{"malformed", "bad", "bad", "2", false},
		{"bad inode", "1:x", "2:x", "2", false},
		{"extra colon", "1:10:2", "2:10:2", "2", false},
		{"signed dev", "-1:10", "2:10", "2", true},
		{"empty", "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchPersistedObject(tc.stored, tc.current, tc.parent); got != tc.want {
				t.Fatalf("match=%v want %v", got, tc.want)
			}
		})
	}
}

func TestPersistedAnchorChainLogic(t *testing.T) {
	chain := func(ids ...string) []PathAnchor {
		a := make([]PathAnchor, len(ids))
		for i, id := range ids {
			a[i] = PathAnchor{Path: string(rune('a' + i)), Identity: id}
		}
		return a
	}
	for _, tc := range []struct {
		name          string
		before, after []PathAnchor
		want          bool
	}{
		{"same", chain("1:1", "1:2", "2:3"), chain("1:1", "1:2", "2:3"), true},
		{"renumber", chain("1:1", "1:2", "2:3"), chain("7:1", "7:2", "8:3"), true},
		{"new mount", chain("1:1", "1:2"), chain("7:1", "8:2"), false},
		{"removed mount", chain("1:1", "2:2"), chain("7:1", "7:2"), false},
		{"inconsistent mapping", chain("1:1", "2:2", "1:3"), chain("7:1", "8:2", "9:3"), false},
		{"inode", chain("1:1"), chain("7:2"), false},
		{"shorter", chain("1:1", "1:2"), chain("7:1"), false},
		{"malformed", chain("bad"), chain("bad"), false},
		{"path", []PathAnchor{{Path: "a", Identity: "1:1"}}, []PathAnchor{{Path: "b", Identity: "7:1"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchPersistedAnchorChain(tc.before, tc.after); got != tc.want {
				t.Fatalf("match=%v want %v", got, tc.want)
			}
		})
	}
}

func TestPersistedIdentityPlatformPolicy(t *testing.T) {
	relaxed := runtime.GOOS == "darwin"
	if got := MatchPersistedDirectory("1:10", "2:10", "2"); got != relaxed {
		t.Fatalf("%s policy=%v", runtime.GOOS, got)
	}
	if err := checkPersistedAnchors([]PathAnchor{{Path: "a", Identity: "1:10"}}, []PathAnchor{{Path: "a", Identity: "2:10"}}); (err == nil) != relaxed {
		t.Fatalf("%s anchor policy: %v", runtime.GOOS, err)
	}
	// Held descriptors always use strict equality, even on Darwin.
	if checkAnchors([]PathAnchor{{Path: "a", Identity: "1:10"}}, []PathAnchor{{Path: "a", Identity: "2:10"}}) == nil {
		t.Fatal("held-fd anchors relaxed")
	}
	file := Identity{Exists: true, SHA256: "same", Mode: 0600}
	if got := matchPurgeEntry(PurgeEntry{ObjectID: "1:10", File: file}, PurgeEntry{ObjectID: "2:10", File: file}, "2"); got != relaxed {
		t.Fatalf("%s regular object policy=%v", runtime.GOOS, got)
	}
	changed := file
	changed.SHA256 = "foreign"
	if matchPurgeEntry(PurgeEntry{ObjectID: "1:10", File: file}, PurgeEntry{ObjectID: "2:10", File: changed}, "2") {
		t.Fatal("changed content accepted")
	}
	if matchPurgeEntry(PurgeEntry{ObjectID: "1:10", File: file}, PurgeEntry{ObjectID: "2:11", File: file}, "2") {
		t.Fatal("replaced object accepted")
	}
	if matchPurgeEntry(PurgeEntry{ObjectID: "1:10", File: file}, PurgeEntry{ObjectID: "2:10", File: file}, "3") {
		t.Fatal("cross-volume object accepted")
	}
}
