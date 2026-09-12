package journal

import "github.com/777genius/agent-notifications/internal/strictjson"

// strictJSON preserves journal repair semantics and historical container budgets.
// File size is bounded by the journal reader before this validation.
func strictJSON(b []byte) error {
	if strictjson.Validate(b, strictjson.Budget{Bytes: max(1, len(b)), Depth: 16, Entries: 10000}) != nil {
		return ErrRepair
	}
	return nil
}
