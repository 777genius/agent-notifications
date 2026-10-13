package opencodeevent

import (
	"math"
	"time"
)

// FILETIME is unsigned 100ns ticks since 1601, not Unix nanoseconds. Return
// zero time on invalid/overflow; the common port rejects its negative Unix sec.
func filetimeWall(low, high uint32) time.Time {
	const unixEpochTicks uint64 = 116_444_736_000_000_000
	ticks := uint64(high)<<32 | uint64(low)
	if ticks < unixEpochTicks {
		return time.Time{}
	}
	ticks -= unixEpochTicks
	if ticks > uint64(math.MaxInt64)/100 {
		return time.Time{}
	}
	return time.Unix(0, int64(ticks)*100)
}
