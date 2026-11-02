package sharedstate

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func checkACL(fd int) error {
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		_, err := unix.Fgetxattr(fd, name, nil)
		if err == unix.ENODATA || err == unix.ENOTSUP {
			continue
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("shared endpoint forbids extended ACLs")
	}
	return nil
}
