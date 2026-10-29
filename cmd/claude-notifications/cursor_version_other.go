//go:build !windows

package main

// Host CLI versions must not gate installation. Test/qualification versions
// describe evidence, not minimums, maximums or exact-version allowlists.
// Keep this source-package placeholder so cache overlays replace the historical
// probe instead of leaving its old implementation in an existing plugin root.
