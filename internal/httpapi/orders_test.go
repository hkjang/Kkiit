package httpapi

import "testing"

func TestOrderTransitionTable(t *testing.T) {
	valid := [][2]string{{"CREATED", "PAYMENT_PENDING"}, {"READY", "IN_PROGRESS"}, {"IN_PROGRESS", "DELIVERED"}, {"DELIVERED", "REVISION_REQUESTED"}, {"DELIVERED", "ACCEPTED"}, {"ACCEPTED", "COMPLETED"}}
	for _, pair := range valid {
		if !orderTransitions[pair[0]][pair[1]] {
			t.Errorf("expected %s -> %s", pair[0], pair[1])
		}
	}
	invalid := [][2]string{{"CREATED", "COMPLETED"}, {"DELIVERED", "PAID"}, {"COMPLETED", "IN_PROGRESS"}, {"REFUNDED", "COMPLETED"}}
	for _, pair := range invalid {
		if orderTransitions[pair[0]][pair[1]] {
			t.Errorf("unexpected %s -> %s", pair[0], pair[1])
		}
	}
}
