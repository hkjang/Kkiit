package httpapi

import "testing"

func TestValidateWebhookInputAcceptsKnownEvents(t *testing.T) {
	in := webhookInput{Name: "내부 연동", TargetURL: "https://hooks.example.com/kkiit", Events: []string{"OrderPAID", "*"}}
	if message, ok := validateWebhookInput(&in, true); !ok {
		t.Fatalf("expected valid webhook, got %q", message)
	}
}

func TestValidateWebhookInputRejectsUnusableTargets(t *testing.T) {
	cases := map[string]webhookInput{
		"이름 없음":   {TargetURL: "https://hooks.example.com", Events: []string{"OrderPAID"}},
		"스킴 없음":   {Name: "연동", TargetURL: "hooks.example.com", Events: []string{"OrderPAID"}},
		"파일 스킴":   {Name: "연동", TargetURL: "file:///etc/passwd", Events: []string{"OrderPAID"}},
		"이벤트 없음":  {Name: "연동", TargetURL: "https://hooks.example.com", Events: []string{}},
		"모르는 이벤트": {Name: "연동", TargetURL: "https://hooks.example.com", Events: []string{"OrderTeleported"}},
	}
	for name, in := range cases {
		input := in
		if _, ok := validateWebhookInput(&input, true); ok {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestValidateWebhookInputHonorsPrivateTargetPolicy(t *testing.T) {
	in := webhookInput{Name: "내부 연동", TargetURL: "http://127.0.0.1:9000/hook", Events: []string{"OrderPAID"}}
	if _, ok := validateWebhookInput(&in, true); !ok {
		t.Fatal("offline deployments must be able to target internal hosts")
	}
	if _, ok := validateWebhookInput(&in, false); ok {
		t.Fatal("private target must be rejected when the policy forbids it")
	}
}

func TestEventCatalogIsUniqueAndCoversOrderStates(t *testing.T) {
	seen := make(map[string]bool, len(eventCatalog))
	for _, event := range eventCatalog {
		if seen[event] {
			t.Fatalf("duplicate event %q", event)
		}
		seen[event] = true
	}
	// applyOrderTransition emits Order<TARGET>, so every reachable target state
	// needs a catalog entry or a webhook subscription silently misses it.
	for state, targets := range orderTransitions {
		for target := range targets {
			if !seen["Order"+target] {
				t.Fatalf("transition %s -> %s has no event in the catalog", state, target)
			}
		}
	}
	if !seen["OrderCreated"] {
		t.Fatal("order creation has no event in the catalog")
	}
}
