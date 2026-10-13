//go:build !linux && !darwin && !windows

package opencodeevent

type systemCounter struct{}

func (systemCounter) SampleCounter() (CounterSample, bool) { return CounterSample{}, false }
