//go:build !windows

package opencodeevent

import "time"

type systemWall struct{}

func (systemWall) SampleWall() time.Time { return time.Now() }
