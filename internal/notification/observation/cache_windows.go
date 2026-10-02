//go:build windows

package observation

import "github.com/777genius/agent-notifications/internal/installruntime"

func checkCacheRoot(root string) error { return installruntime.CheckPrivateCacheRoot(root) }
func readCache(root string) ([]byte, error) {
	return installruntime.ReadPrivateCacheDocument(root, "observations.json", cacheBytes)
}
func writeCache(root string, data []byte) error {
	// Cooperating readers and writers retain the permanent kernel lock. A crash
	// between removal and exclusive publication can lose this recent cache;
	// cache loss is explicitly outside the bounded duplicate guarantee.
	if err := installruntime.RemoveConfinedName(root, "observations.json"); err != nil {
		return err
	}
	return installruntime.WriteConfinedExclusive(root, "observations.json", data)
}
