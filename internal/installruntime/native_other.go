//go:build !darwin

package installruntime

import (
	"context"
	"fmt"
)

func verifyNativePlatform(ctx context.Context, bundle string) error {
	return fmt.Errorf("native signature/architecture verification requires Darwin")
}
