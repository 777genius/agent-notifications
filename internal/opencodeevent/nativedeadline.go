package opencodeevent

import (
	"math"
	"strings"

	"github.com/777genius/agent-notifications/internal/notification"
)

// BridgeNativeDeadline preserves transport UUID spelling and NotAfter. It may
// only be used under a clock cell proving native RAW/Mach coordinate equality.
func BridgeNativeDeadline(d notification.Deadline, rawBoot string) (notification.Deadline, error) {
	if !validClockUUID(d.BootID) || !validClockUUID(strings.ToLower(rawBoot)) || strings.ToLower(rawBoot) != d.BootID ||
		math.IsNaN(d.NotAfter) || math.IsInf(d.NotAfter, 0) || d.NotAfter <= 0 {
		return notification.Deadline{}, ErrClockUnavailable
	}
	d.BootID = rawBoot
	return d, nil
}
