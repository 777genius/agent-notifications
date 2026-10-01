//go:build !linux

package main

import (
	"context"
	"errors"
)

func verifyRuntimeLiveImage(context.Context, runtimeProfileInput) (runtimeLiveImage, error) {
	return runtimeLiveImage{}, errors.New("live_image_unverified")
}
