//go:build darwin && cgo

package notifier

/*
#include <mach/mach_time.h>
static double notify_continuous_seconds(void) {
 mach_timebase_info_data_t info;
 if (mach_timebase_info(&info) != KERN_SUCCESS || info.denom == 0) return -1;
 return (double)mach_continuous_time() * (double)info.numer / (double)info.denom / 1000000000.0;
}
*/
import "C"

import (
	"errors"
	"golang.org/x/sys/unix"
)

type SystemBootClock struct{}

func (SystemBootClock) Now() (string, float64, error) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", 0, err
	}
	seconds := float64(C.notify_continuous_seconds())
	if seconds < 0 || boot == "" {
		return "", 0, errors.New("continuous clock unavailable")
	}
	return boot, seconds, nil
}
