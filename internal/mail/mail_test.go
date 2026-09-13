package mail

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hkjang/Kkiit/internal/mail/mailtest"
)

func relayConfig(relay *mailtest.Server) Config {
	config := ReadConfig(map[string]any{"enabled": true, "smtp_host": relay.Host(), "smtp_port": float64(relay.Port()), "from_address": "kkiit@corp.example", "from_name": "Kkiit 알림", "base_url": "https://kkiit.corp.example/"}, "")
	config.Timeout = 3 * time.Second
	return config
}

func TestDefaultsMatchAnInternalRelay(t *testing.T) {
	config := ReadConfig(map[string]any{}, "")
	if config.Enabled {
		t.Fatal("mail must be off by default")
	}
	if config.Port != 25 || config.Security != "auto" || config.Timeout != 10*time.Second || config.SkipVerify || config.Username != "" {
		t.Fatalf("defaults are not port 25 · auto · 10s · no auth: %+v", config)
	}
	for _, event := range Events {
		if !config.Allows(event.Type) {
			t.Fatalf("%s must be on unless switched off", event.Type)
		}
	}
	if config.Allows("OrderCreated") || config.Allows("MessageCreated") {
		t.Fatal("events outside the list are never mailed")
	}
	defaults := DefaultValues()
	for _, event := range Events {
		if defaults[event.Setting] != true {
			t.Fatalf("default row must switch %s on", event.Setting)
		}
	}
	if defaults["enabled"] != false || defaults["smtp_port"] != float64(25) {
		t.Fatalf("default row differs from the relay defaults: %v", defaults)
	}
}

func TestReadConfigHonoursSwitchesAndImplicitTLSPort(t *testing.T) {
	config := ReadConfig(map[string]any{"enabled": true, "smtp_host": "relay.internal", "smtp_port": float64(465), "notify_order_paid": false, "timeout_seconds": float64(30)}, "hunter2")
	if config.Security != "tls" {
		t.Fatalf("port 465 must imply tls, got %q", config.Security)
	}
	if config.Allows("OrderPAID") || !config.Allows("OrderDELIVERED") {
		t.Fatal("a switched off event must stop only itself")
	}
	if config.Password != "hunter2" || config.Timeout != 30*time.Second {
		t.Fatalf("password or timeout not read: %+v", config)
	}
	if config.FromAddress != "kkiit@relay.internal" {
		t.Fatalf("from address must fall back to the relay host, got %q", config.FromAddress)
	}
}

func TestValidateNamesTheMissingField(t *testing.T) {
	for _, testCase := range []struct {
		values map[string]any
		want   string
	}{
		{map[string]any{}, "mail.smtp_host"},
		{map[string]any{"smtp_host": "relay", "smtp_port": float64(70000)}, "mail.smtp_port"},
		{map[string]any{"smtp_host": "relay", "from_address": "nobody"}, "mail.from_address"},
		{map[string]any{"smtp_host": "relay", "security": "ssl"}, "mail.security"},
		{map[string]any{"smtp_host": "relay", "timeout_seconds": float64(600)}, "mail.timeout_seconds"},
	} {
		err := ReadConfig(testCase.values, "").Validate()
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%v: want %s in error, got %v", testCase.values, testCase.want, err)
		}
	}
	if err := ReadConfig(map[string]any{"smtp_host": "relay"}, "").Validate(); err != nil {
		t.Fatalf("host alone is a complete configuration for an internal relay: %v", err)
	}
}

func TestDigestFoldsSeveralItemsIntoOneMessage(t *testing.T) {
	config := ReadConfig(map[string]any{"from_name": "Kkiit", "base_url": "https://kkiit.corp.example/"}, "")
	one := Digest(config, "seller@corp.example", []Item{{Subject: "결제 완료 KK-1", Body: "주문 결제가 확인되었습니다.", Link: "/orders/1"}})
	if one.Subject != "[Kkiit] 결제 완료 KK-1" {
		t.Fatalf("single subject=%q", one.Subject)
	}
	if !strings.Contains(one.Body, "바로 열기: https://kkiit.corp.example/orders/1") || strings.Contains(one.Body, "■") {
		t.Fatalf("single body must carry the absolute link and no section marker:\n%s", one.Body)
	}
	if !strings.Contains(one.Body, "https://kkiit.corp.example/profile/notifications") {
		t.Fatalf("footer must point at the preference screen:\n%s", one.Body)
	}
	many := Digest(config, "seller@corp.example", []Item{{Subject: "결제 완료 KK-1", Body: "a"}, {Subject: "견적 선택", Body: "b", Link: "/profile/projects"}})
	if many.Subject != "[Kkiit] 알림 2건: 결제 완료 KK-1 외 1건" {
		t.Fatalf("digest subject=%q", many.Subject)
	}
	if !strings.Contains(many.Body, "■ 결제 완료 KK-1") || !strings.Contains(many.Body, "■ 견적 선택") {
		t.Fatalf("digest body must list every item:\n%s", many.Body)
	}
	if got := ReadConfig(map[string]any{}, "").Link("/orders/1"); got != "" {
		t.Fatalf("without a base address no link is rendered, got %q", got)
	}
}

func TestDeliverReachesAnUnauthenticatedRelay(t *testing.T) {
	relay := mailtest.Start(t)
	config := relayConfig(relay)
	body := "첫 줄\n.으로 시작하는 줄\n마지막 줄"
	if err := Deliver(config, Message{To: "seller@corp.example", Subject: "결제 완료 KK-1", Body: body}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	messages := relay.Messages()
	if len(messages) != 1 {
		t.Fatalf("relay received %d messages", len(messages))
	}
	received := messages[0]
	if received.From != "kkiit@corp.example" || len(received.To) != 1 || received.To[0] != "seller@corp.example" {
		t.Fatalf("envelope=%+v", received)
	}
	if !strings.Contains(received.Data, "Subject: =?utf-8?q?") || !strings.Contains(received.Data, "From: =?utf-8?q?Kkiit_=EC=95=8C=EB=A6=BC?= <kkiit@corp.example>") {
		t.Fatalf("headers must be encoded for non-ASCII:\n%s", received.Data)
	}
	if !strings.Contains(received.Data, "\r\n.으로 시작하는 줄\r\n") {
		t.Fatalf("a line starting with a dot must arrive with exactly one dot:\n%s", received.Data)
	}
	if !strings.Contains(received.Data, "Auto-Submitted: auto-generated") {
		t.Fatalf("notification mail must be marked auto-submitted:\n%s", received.Data)
	}
}

func TestDeliverAuthenticatesWhenTheRelayDemandsIt(t *testing.T) {
	relay := mailtest.Start(t)
	relay.Username, relay.Password = "kkiit", "secret"
	config := relayConfig(relay)
	config.Username, config.Password = "kkiit", "secret"
	if err := Deliver(config, Message{To: "a@corp.example", Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("deliver with credentials: %v", err)
	}
	config.Password = "wrong"
	if err := Deliver(config, Message{To: "a@corp.example", Subject: "s", Body: "b"}); err == nil || !strings.Contains(err.Error(), "인증 실패") {
		t.Fatalf("wrong password must fail at AUTH, got %v", err)
	}
	config.Security = "starttls"
	config.Password = "secret"
	if err := Deliver(config, Message{To: "a@corp.example", Subject: "s", Body: "b"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("demanding STARTTLS from a relay without it must be a configuration error, got %v", err)
	}
}

func TestDeliverReportsARefusingOrDeadRelay(t *testing.T) {
	relay := mailtest.Start(t)
	relay.Reject = true
	config := relayConfig(relay)
	if err := Deliver(config, Message{To: "a@corp.example", Subject: "s", Body: "b"}); err == nil || !strings.Contains(err.Error(), "MAIL FROM") {
		t.Fatalf("refused envelope must name the step, got %v", err)
	}
	relay.Close()
	config.Timeout = time.Second
	started := time.Now()
	if err := Deliver(config, Message{To: "a@corp.example", Subject: "s", Body: "b"}); err == nil || !strings.Contains(err.Error(), "SMTP 연결 실패") {
		t.Fatalf("dead relay must fail at connect, got %v", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("a dead relay must fail within the timeout")
	}
	if err := Deliver(Config{}, Message{To: "a@corp.example"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty configuration must not dial, got %v", err)
	}
}
