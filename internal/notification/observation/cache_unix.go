//go:build linux || darwin

package observation

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// This is a narrow private-file adapter to the existing installation lock.
// No setup, directory creation or durable event journal lives here.
func checkCacheRoot(root string) error {
	for p := root; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			return errors.New("unsafe_cache")
		}
		if p == root && info.Mode().Perm() != 0700 {
			return errors.New("unsafe_cache")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) {
		return errors.New("unsafe_cache")
	}
	return nil
}

func readCache(root string) ([]byte, error) {
	fd, err := unix.Open(filepath.Join(root, "observations.json"), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "observations.json")
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Size > cacheBytes {
		return nil, errors.New("unsafe_cache")
	}
	return io.ReadAll(io.LimitReader(f, cacheBytes+1))
}

func writeCache(root string, data []byte) error {
	return writeCacheContext(context.Background(), root, data)
}

func writeCacheContext(ctx context.Context, root string, data []byte) error {
	cache := &pathCache{root: root}
	defer cache.Close()
	if err := cache.Prepare(ctx); err != nil {
		return err
	}
	return cache.Write(ctx, data)
}

type pathCache struct {
	root string
	temp string
	file *os.File
}

func openCache(root string) (*pathCache, error) {
	if err := checkCacheRoot(root); err != nil {
		return nil, err
	}
	return &pathCache{root: root}, nil
}
func (c *pathCache) Read() ([]byte, error) { return readCache(c.root) }
func (c *pathCache) Prepare(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := filepath.Join(c.root, ".observations-"+uuid.NewString())
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	c.temp, c.file = path, f
	if err := ctx.Err(); err != nil {
		c.Close()
		return err
	}
	return nil
}
func (c *pathCache) Close() {
	if c.file != nil {
		_ = c.file.Close()
		c.file = nil
	}
	if c.temp != "" {
		_ = os.Remove(c.temp)
		c.temp = ""
	}
}
func (c *pathCache) Write(ctx context.Context, data []byte) error {
	if c.file == nil {
		return errors.New("cache preparation is closed")
	}
	defer c.Close()
	n, err := c.file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = c.file.Sync()
	}
	closeErr := c.file.Close()
	c.file = nil
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(c.temp, filepath.Join(c.root, "observations.json")); err != nil {
		return err
	}
	c.temp = ""
	return nil
}
