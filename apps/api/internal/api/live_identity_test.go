package api

import (
	"strings"
	"testing"
)

func TestLivePlanIDsAreCollisionResistant(t *testing.T) {
	const count = 1000
	ids := make(map[string]struct{}, count)
	for range count {
		id := newLivePlanID()
		if !strings.HasPrefix(id, "plan-") {
			t.Fatalf("live plan ID %q does not retain plan prefix", id)
		}
		if _, exists := ids[id]; exists {
			t.Fatalf("duplicate live plan ID %q", id)
		}
		ids[id] = struct{}{}
	}
}
