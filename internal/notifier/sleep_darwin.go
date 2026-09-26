//go:build darwin

package notifier

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>
*/
import "C"

// activeDisplaySleepStates is a seam so tests can drive evaluateDisplaySleep
// without a real display to put to sleep.
var activeDisplaySleepStates = defaultActiveDisplaySleepStates

// desktopDisplayIsAsleep reports whether every active macOS display is
// currently asleep, via the public Quartz Display Services API
// (CGGetActiveDisplayList / CGDisplayIsAsleep) rather than the undocumented
// IOKit power-management dictionaries that other tools scrape: those live
// under IODisplayWrangler on Intel Macs, but Apple Silicon's display power
// state is not exposed there, which made that approach unreliable across the
// fleet this plugin actually runs on.
func desktopDisplayIsAsleep() bool {
	states, ok := activeDisplaySleepStates()
	if !ok {
		return false
	}
	return evaluateDisplaySleep(states)
}

// evaluateDisplaySleep is the pure decision layer: sound is muted only when
// every active display is asleep and at least one display was found.
//
// It checks every active display rather than only the main one: on a laptop
// running in clamshell mode with an external monitor attached, the built-in
// display reports asleep while the user is still actively working on the
// external one, and muting sound in that case would be the exact "swallowed
// cue" failure this plugin's Do Not Disturb detection is already deliberately
// biased against (see dnd.go).
func evaluateDisplaySleep(states []bool) bool {
	if len(states) == 0 {
		return false
	}
	for _, asleep := range states {
		if !asleep {
			return false
		}
	}
	return true
}

// defaultActiveDisplaySleepStates asks Quartz Display Services which displays
// are currently active and whether each one is asleep. Any failure to
// enumerate displays returns ok=false, which desktopDisplayIsAsleep treats as
// "not asleep" so the query can never turn into a missed sound.
func defaultActiveDisplaySleepStates() ([]bool, bool) {
	var count C.uint32_t
	if C.CGGetActiveDisplayList(0, nil, &count) != C.kCGErrorSuccess || count == 0 {
		return nil, false
	}

	ids := make([]C.CGDirectDisplayID, count)
	if C.CGGetActiveDisplayList(count, &ids[0], &count) != C.kCGErrorSuccess || count == 0 {
		return nil, false
	}

	states := make([]bool, count)
	for i, id := range ids[:count] {
		states[i] = C.CGDisplayIsAsleep(id) != 0
	}
	return states, true
}
