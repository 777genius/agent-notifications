//go:build !darwin

package installruntime

import "testing"

func TestRenumberDarwinFilesystemDeferredToCI(t *testing.T) {
	t.Skip("Darwin renumber, refresh, rollback, purge and mount behavior requires ephemeral macOS CI; Linux comparator tests are logic evidence only")
}
