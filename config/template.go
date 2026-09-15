// Package configtemplate owns the packaged default document used by future Store
// initialization. The shipping path remains config/config.json.
package configtemplate

import _ "embed"

//go:embed config.json
var template []byte

// Bytes returns an independent copy; callers cannot mutate the embedded seed.
func Bytes() []byte { return append([]byte(nil), template...) }
