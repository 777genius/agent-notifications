//go:build !windows

package windowscallback

import "context"

func (Port) CheckReadiness(ctx context.Context, _ Binding, _ uint64) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrUnavailable
}
