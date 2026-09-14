package portable

import (
	"os"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// SameInstalledFile is true when current and installed name the same regular
// file. Darwin /var vs /private/var aliases compare equal; a copy does not.
func SameInstalledFile(current, installed string) bool {
	if current == "" || installed == "" {
		return false
	}
	if current == installed {
		return true
	}
	a, err := os.Stat(current)
	if err != nil || !a.Mode().IsRegular() {
		return false
	}
	b, err := os.Stat(installed)
	if err != nil || !b.Mode().IsRegular() {
		return false
	}
	return os.SameFile(a, b)
}

func samePhysicalPath(a, b string) bool {
	if a == b {
		return true
	}
	left, err := installruntime.PhysicalPath(a)
	if err != nil {
		return false
	}
	right, err := installruntime.PhysicalPath(b)
	if err != nil {
		return false
	}
	return left == right
}
