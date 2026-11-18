package installruntime

import "testing"

// OpenCode's event/managed-config readers must survive a Windows callback
// promotion without accepting an unknown or mismatched writer protocol.
func TestOpenCodeLedgerCompatibleKnownVersions(t *testing.T) {
	tests := []struct {
		name   string
		schema int
		floor  int
		accept bool
	}{
		{"original-v4", 4, OpenCodeWriterFloor, true},
		{"windows-generation-v6", WindowsGenerationLedgerSchema, WindowsGenerationWriterFloor, true},
		{"v4-promoted-floor", 4, 6, false},
		{"windows-v6-old-floor", 6, 4, false},
		{"windows-v6-intermediate-floor", 6, 5, false},
		{"windows-v6-unknown-future-floor", 6, 7, false},
		{"unsupported-v5", 5, 5, false},
		{"uninitialized", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OpenCodeLedgerCompatible(Ledger{Schema: tt.schema, WriterFloor: tt.floor}); got != tt.accept {
				t.Fatalf("schema %d writer floor %d: accepted=%v want=%v", tt.schema, tt.floor, got, tt.accept)
			}
		})
	}
}
