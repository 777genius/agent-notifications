package installruntime

import (
	"fmt"
	"strconv"
	"strings"
)

// Unix identities are decimal dev:ino; Darwin's dev_t can be signed.
func splitObjectID(id string) (dev, ino string, ok bool) {
	dev, ino, ok = strings.Cut(id, ":")
	if !ok {
		return "", "", false
	}
	n, err := strconv.ParseUint(ino, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != ino {
		return "", "", false
	}
	magnitude := strings.TrimPrefix(dev, "-")
	d, err := strconv.ParseUint(magnitude, 10, 64)
	if err != nil || strconv.FormatUint(d, 10) != magnitude || dev == "-0" {
		return "", "", false
	}
	return dev, ino, true
}

func objectDevice(id string) string {
	dev, _, ok := splitObjectID(id)
	if !ok {
		return ""
	}
	return dev
}

// MatchPersistedDirectory compares a durable identity with a freshly observed
// physical directory. Callers must reject symlinks/non-directories first and
// supply the device of its held or no-follow observed parent. Only Darwin may
// renumber devices, and only away from a current volume boundary.
func MatchPersistedDirectory(stored, current, parentDev string) bool {
	if !persistedDevRelaxed {
		return stored == current
	}
	return matchPersistedObject(stored, current, parentDev)
}

func matchPersistedObject(stored, current, parentDev string) bool {
	sd, si, sok := splitObjectID(stored)
	cd, ci, cok := splitObjectID(current)
	return sok && cok && si == ci && (sd == cd || cd == parentDev)
}

// Preserve both the inode chain and its volume partition. A bijection prevents
// a new mount boundary or merged volumes from masquerading as device renumbering.
func matchPersistedAnchorChain(want, got []PathAnchor) bool {
	if len(got) < len(want) {
		return false
	}
	forward, reverse := map[string]string{}, map[string]string{}
	for i, a := range want {
		b := got[i]
		ad, ai, aok := splitObjectID(a.Identity)
		bd, bi, bok := splitObjectID(b.Identity)
		if a.Path != b.Path || !aok || !bok || ai != bi {
			return false
		}
		if d, ok := forward[ad]; ok && d != bd {
			return false
		}
		if d, ok := reverse[bd]; ok && d != ad {
			return false
		}
		forward[ad], reverse[bd] = bd, ad
	}
	return true
}

func checkPersistedAnchors(want, got []PathAnchor) error {
	if !persistedDevRelaxed {
		return checkAnchors(want, got)
	}
	if !matchPersistedAnchorChain(want, got) {
		return fmt.Errorf("managed destination persisted parent substituted")
	}
	return nil
}
