//go:build !linux && !darwin && !windows

package observation

import "errors"

func checkCacheRoot(string) error      { return errors.New("cache_unavailable") }
func readCache(string) ([]byte, error) { return nil, errors.New("cache_unavailable") }
func writeCache(string, []byte) error  { return errors.New("cache_unavailable") }

type pathCache struct{ root string }

func openCache(root string) (*pathCache, error) {
	if err := checkCacheRoot(root); err != nil {
		return nil, err
	}
	return &pathCache{root: root}, nil
}
func (c *pathCache) Read() ([]byte, error)   { return readCache(c.root) }
func (c *pathCache) Write(data []byte) error { return writeCache(c.root, data) }
func (c *pathCache) Close()                  {}
