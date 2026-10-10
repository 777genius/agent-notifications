//go:build !windows

package installruntime

func orphanPermanentLockIdentity(path string) (string, error) {
	return regularObjectID(path)
}
