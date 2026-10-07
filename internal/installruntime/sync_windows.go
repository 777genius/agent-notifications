package installruntime

// Windows directory handles cannot be synced through os.File. File contents
// are flushed before rename; power-loss durability requires OS qualification.
func syncDir(path string) error { return nil }

// Windows exposes the DOS readonly bit, not Unix execute/group permission bits.
// Compare the permissions the OS can retain so crash replay remains idempotent.
func identityMode(mode uint32) uint32 {
	if mode&0200 == 0 {
		return 0444
	}
	return 0666
}
