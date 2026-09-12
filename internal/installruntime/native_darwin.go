package installruntime

import (
	"context"
	"debug/macho"
	"fmt"
	"path/filepath"
	"runtime"
)

func verifyNativePlatform(ctx context.Context, bundle string) error {
	if _, err := boundedCommand(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", "-R", `=identifier "com.claude.desktop.notifier"`, bundle); err != nil {
		return fmt.Errorf("native signed offline manifest verification: %w", err)
	}
	path := filepath.Join(bundle, "Contents", "MacOS", "terminal-notifier-modern")
	want := macho.CpuAmd64
	if runtime.GOARCH == "arm64" {
		want = macho.CpuArm64
	}
	if fat, err := macho.OpenFat(path); err == nil {
		defer fat.Close()
		for _, arch := range fat.Arches {
			if arch.Cpu == want {
				return nil
			}
		}
		return fmt.Errorf("native architecture unavailable")
	}
	thin, err := macho.Open(path)
	if err != nil {
		return err
	}
	defer thin.Close()
	if thin.Cpu != want {
		return fmt.Errorf("native architecture mismatch")
	}
	return nil
}
