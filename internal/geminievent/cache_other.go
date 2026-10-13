//go:build !linux && !darwin && !windows

package geminievent

import "errors"

func checkCacheRoot(string) error      { return errors.New("cache_unavailable") }
func readCache(string) ([]byte, error) { return nil, errors.New("cache_unavailable") }
func writeCache(string, []byte) error  { return errors.New("cache_unavailable") }
