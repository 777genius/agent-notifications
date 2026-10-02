package installruntime

import "testing"

func chain(ids ...string) []PathAnchor {
	paths := []string{"/a", "/a/b", "/a/b/c"}
	out := make([]PathAnchor, len(ids))
	for i, id := range ids {
		out[i] = PathAnchor{Path: paths[i], Identity: id}
	}
	return out
}

func TestDeviceRenumbering(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stored, fresh []PathAnchor
		ok, renumber  bool
	}{
		{"unchanged", chain("5:2", "5:3"), chain("5:2", "5:3"), true, false},
		{"one volume renumbered", chain("6:2", "6:3"), chain("5:2", "5:3"), true, true},
		{"two volumes renumbered", chain("6:2", "9:3"), chain("5:2", "8:3"), true, true},
		{"recorded chain is a prefix", chain("6:2"), chain("5:2", "5:3"), true, true},
		{"one old volume now on two devices", chain("6:2", "6:3"), chain("5:2", "7:3"), false, false},
		{"two old volumes now on one device", chain("6:2", "9:3"), chain("5:2", "5:3"), false, false},
		{"different inode", chain("6:2", "6:4"), chain("5:2", "5:3"), false, false},
		{"recorded chain longer", chain("6:2", "6:3", "6:4"), chain("5:2", "5:3"), false, false},
		{"windows identities", chain("1:0:2"), chain("1:0:2"), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newDeviceRenumbering()
			if got := m.add(tc.stored, tc.fresh); got != tc.ok {
				t.Fatalf("add = %v, want %v", got, tc.ok)
			}
			if tc.ok && m.renumbered() != tc.renumber {
				t.Fatalf("renumbered = %v, want %v", m.renumbered(), tc.renumber)
			}
		})
	}
	t.Run("different path", func(t *testing.T) {
		stored := []PathAnchor{{Path: "/a", Identity: "6:2"}}
		fresh := []PathAnchor{{Path: "/z", Identity: "5:2"}}
		if newDeviceRenumbering().add(stored, fresh) {
			t.Fatal("chain with a different path accepted")
		}
	})
	t.Run("one mapping across chains", func(t *testing.T) {
		m := newDeviceRenumbering()
		if !m.add(chain("6:2"), chain("5:2")) {
			t.Fatal("first chain refused")
		}
		if m.add(chain("6:2", "6:7"), chain("5:2", "7:7")) {
			t.Fatal("second chain remapped a known device")
		}
	})
	t.Run("rekey", func(t *testing.T) {
		m := newDeviceRenumbering()
		if !m.add(chain("6:2"), chain("5:2")) {
			t.Fatal("chain refused")
		}
		for stored, want := range map[string]string{"6:9": "5:9", "8:9": "8:9", "": "", "1:0:2": "1:0:2"} {
			if got := m.rekey(stored); got != want {
				t.Fatalf("rekey(%q) = %q, want %q", stored, got, want)
			}
		}
	})
}
