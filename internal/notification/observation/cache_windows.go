//go:build windows

package observation

import (
	"context"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func checkCacheRoot(root string) error { return installruntime.CheckPrivateCacheRoot(root) }
func readCache(root string) ([]byte, error) {
	return installruntime.ReadPrivateCacheDocument(root, "observations.json", cacheBytes)
}
func writeCache(root string, data []byte) error {
	return installruntime.WritePrivateCacheDocument(root, "observations.json", data)
}

type windowsCache struct {
	root     *installruntime.PrivateCacheRoot
	prepared *installruntime.PrivateCacheWrite
}

func openCache(root string) (*windowsCache, error) {
	session, err := installruntime.OpenPrivateCacheRoot(root)
	if err != nil {
		return nil, err
	}
	return &windowsCache{root: session}, nil
}
func (c *windowsCache) Read() ([]byte, error) { return c.root.Read("observations.json", cacheBytes) }
func (c *windowsCache) Prepare(ctx context.Context) error {
	var err error
	c.prepared, err = c.root.PrepareWrite(ctx, "observations.json")
	return err
}
func (c *windowsCache) Write(ctx context.Context, data []byte) error {
	return c.prepared.WriteContext(ctx, data)
}
func (c *windowsCache) Close() {
	_ = c.prepared.Close()
	c.root.Close()
}
