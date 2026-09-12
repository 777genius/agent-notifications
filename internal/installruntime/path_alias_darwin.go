package installruntime

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// macOS exposes these root-owned aliases in standard temporary/config paths.
// Resolve only the fixed OS mapping; arbitrary user-directory links are never
// canonicalized by the descriptor walk.
func platformAnchorPath(path string) (string, error) {
	for _, alias := range []string{"/var", "/tmp", "/etc"} {
		if path != alias && !strings.HasPrefix(path, alias+"/") {
			continue
		}
		info, err := os.Lstat(alias)
		if err != nil {
			return "", err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode()&os.ModeSymlink == 0 {
			return "", fmt.Errorf("unqualified macOS system alias")
		}
		target, err := os.Readlink(alias)
		if err != nil {
			return "", err
		}
		if target != "private"+alias && target != "/private"+alias {
			return "", fmt.Errorf("unexpected macOS system alias target")
		}
		return "/private" + path, nil
	}
	return path, nil
}
