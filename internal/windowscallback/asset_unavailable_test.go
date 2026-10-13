//go:build !windows || !amd64 || !windows_callback_asset

package windowscallback

import "testing"

func TestMissingCompiledAssetUnavailable(t *testing.T) {
	if _, _, e := TrustedAsset(); e != ErrUnavailable {
		t.Fatal("missing compiled asset was enabled", e)
	}
}
