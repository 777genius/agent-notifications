// Package thirdpartynotices provides the canonical terminal dependency notices
// for the executable and its distribution packages.
package thirdpartynotices

import _ "embed"

// Filename is the stable basename in portable packages and release assets.
const Filename = "THIRD_PARTY_NOTICES.txt"

//go:embed THIRD_PARTY_NOTICES.txt
var text string

// Text returns the immutable notice text from the pinned dependency sources.
func Text() string { return text }
