package installruntime

import "fmt"

const (
	// Operator-editable control JSON shares the 64 KiB budget used by
	// decodeUserPolicy. ReadPolicySnapshot must reject oversized documents
	// before allocating the file.
	maxControlDocument = 64 * 1024
	// Native helper hashing and recovery copies of managed files. This is a
	// hard cap against unbounded ReadAll, not the JSON document budget.
	maxManagedFile = 32 << 20
)

// PathAnchor binds a staged destination's existing parents to their opened
// filesystem identities. Recovery must not reinterpret a substituted directory.
type PathAnchor struct{ Path, Identity string }

// PhysicalPath rewrites only Darwin root-owned /var, /tmp, and /etc aliases.
// Arbitrary user-directory symlinks are not canonicalized.
func PhysicalPath(path string) (string, error) {
	return platformAnchorPath(path)
}

func checkAnchors(want, got []PathAnchor) error {
	for i, a := range want {
		if i >= len(got) || a != got[i] {
			return fmt.Errorf("managed destination parent substituted: %s", a.Path)
		}
	}
	return nil
}
