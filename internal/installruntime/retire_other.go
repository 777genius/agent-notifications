//go:build !darwin

package installruntime

import (
	"context"
	"fmt"
)

func verifyNativeDrain(context.Context, string) error {
	return fmt.Errorf("native resource drain requires supported Darwin lsof qualification")
}
