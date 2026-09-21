//go:build !linux && !darwin && !windows

package journal

type PlatformClock struct{}

func DefaultClock() Clock { return PlatformClock{} }

func (PlatformClock) Sample() Sample { return Sample{} }
