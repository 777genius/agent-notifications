//go:build windows

package observation

import "github.com/777genius/agent-notifications/internal/installruntime"

func checkCacheRoot(root string) error { return installruntime.CheckPrivateCacheRoot(root) }
func readCache(root string) ([]byte, error) {
	return installruntime.ReadPrivateCacheDocument(root, "observations.json", cacheBytes)
}
func writeCache(root string, data []byte) error {
	return installruntime.WritePrivateCacheDocument(root, "observations.json", data)
}
