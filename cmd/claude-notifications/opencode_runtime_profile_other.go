//go:build !linux && !windows && (!darwin || !cgo)

package main

import (
	"context"
	"errors"
)

func verifyRuntimeLiveImage(context.Context, runtimeProfileInput) (runtimeLiveImage, error) {
	return runtimeLiveImage{}, errors.New("live_image_unverified")
}

func holdRuntimeLiveImage(context.Context, runtimeProfileInput) (*runtimeImageLease, error) {
	return nil, errors.New("live_image_unverified")
}
