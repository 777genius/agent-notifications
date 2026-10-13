package installprogress

import "os"

func openEvents(path string) (*os.File, error) { return os.Open(path) }

// The private renderer is borrowed by the Unix shell installer. Windows has
// no shell owner watchdog; context cancellation and the deadline still bound it.
func ownerAlive(pid int) bool { return true }

// Windows ownership is enforced by the caller's private directory ACL; Unix
// permission bits have no ownership meaning on this adapter.
func privateOwner(info os.FileInfo) bool { return true }
