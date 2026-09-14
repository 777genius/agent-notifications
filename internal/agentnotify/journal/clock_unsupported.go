//go:build !linux && !darwin && !windows

package journal

type PlatformClock struct{}

func (PlatformClock) Sample() Sample { return Sample{} }
