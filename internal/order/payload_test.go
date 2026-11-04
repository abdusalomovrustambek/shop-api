package order

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestJobPayloadCarriesNoCardData guards the billing.jobs payload shape without
// a database: it marshals jobPayload exactly as Checkout builds it and checks
// the JSON a worker would read carries no card data (CWE-312). The card the
// buyer sends lives only in memory during checkout and must never be persisted.
func TestJobPayloadCarriesNoCardData(t *testing.T) {
	payload := jobPayload{
		OrderID:    1,
		UserID:     "aaaaaaaa-0000-0000-0000-000000000001",
		Currency:   "USD",
		TotalCents: 3600,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal job payload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal job payload: %v", err)
	}
	for _, key := range []string{"card", "number", "exp", "cvc", "pan"} {
		if _, ok := decoded[key]; ok {
			t.Errorf("job payload contains %q: %s", key, raw)
		}
	}
	for _, key := range []string{"order_id", "user_id", "currency", "total_cents"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("job payload is missing %q: %s", key, raw)
		}
	}
	if strings.Contains(string(raw), "4111111111111111") {
		t.Errorf("job payload contains a card number: %s", raw)
	}
}
