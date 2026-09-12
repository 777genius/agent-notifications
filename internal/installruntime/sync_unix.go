//go:build !windows

package installruntime

import "os"

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func identityMode(mode uint32) uint32 { return mode & 0777 }

func windowsReplacementIdentity(path string) (string, error) { return "", nil }
func replacementFingerprint(file File) (Identity, error)     { return Fingerprint(file.Path) }

func checkReplacementRollback(file File) error { return nil }
