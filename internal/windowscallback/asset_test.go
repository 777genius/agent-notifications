//go:build windows && amd64 && windows_callback_asset

package windowscallback

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// Breakage: release generation embeds bytes from one build and a digest from
// another, or exposes its mutable authoritative buffer to setup consumers.
func TestCompiledAssetCustody(t *testing.T) {
	b, d, e := TrustedAsset()
	if e != nil {
		t.Fatal(e)
	}
	if len(b) < 2 || string(b[:2]) != "MZ" {
		t.Fatal("compiled PE bytes absent")
	}
	actual := sha256.Sum256(b)
	if hex.EncodeToString(actual[:]) != d {
		t.Fatal("trusted asset digest mismatch")
	}
	b[0] ^= 255
	again, againDigest, e := TrustedAsset()
	if e != nil || againDigest != d || string(again[:2]) != "MZ" {
		t.Fatal("caller mutated authoritative bytes", e)
	}
}
