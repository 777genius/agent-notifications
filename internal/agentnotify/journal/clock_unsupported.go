//go:build !linux && !darwin

package journal

type PlatformClock struct{}

func (PlatformClock) Sample() Sample { return Sample{} }
