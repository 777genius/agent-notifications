//go:build linux

package main

import "context"

// Keep the exact reviewed Linux proof and comparison. No new Linux authority.
func holdRuntimeLiveImage(ctx context.Context, in runtimeProfileInput) (*runtimeImageLease, error) {
	image, err := verifyRuntimeLiveImage(ctx, in)
	if err != nil {
		return nil, err
	}
	return &runtimeImageLease{origin: ctx, image: image, read: func(ctx context.Context) (runtimeLiveImage, error) { return verifyRuntimeLiveImage(ctx, in) }}, nil
}
