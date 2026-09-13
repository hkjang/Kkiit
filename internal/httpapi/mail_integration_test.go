package httpapi

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hkjang/Kkiit/internal/mail"
	"github.com/hkjang/Kkiit/internal/mail/mailtest"
)

// decodeSubject reads the Subject header back out of a raw message.
func decodeSubject(t *testing.T, data string) string {
	t.Helper()
	for _, line := range strings.Split(data, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			subject, err := new(mime.WordDecoder).DecodeHeader(strings.TrimPrefix(line, "Subject: "))
			if err != nil {
				t.Fatalf("decode subject: %v", err)
			}
			return subject
		}
	}
	return ""
}

func mailsTo(relay *mailtest.Server, address string) []mailtest.Received {
	var matched []mailtest.Received
	for _, message := range relay.Messages() {
		for _, to := range message.To {
			if strings.EqualFold(to, address) {
				matched = append(matched, message)
			}
		}
	}
	return matched
}

// The mail standard in one test: off by default, the password never comes
// back, the test button proves the relay, event mail leaves in the background
// with the actor left out and one message per person, a switched off event
// stays quiet, and a dead relay costs the request nothing.
func TestIntegrationMailNotificationsLeaveThroughTheRelay(t *testing.T) {
	server, pool := integrationServer(t)
	background := context.Background()
	relay := mailtest.Start(t)
	relay.Username, relay.Password = "kkiit", "relay-secret"

	admin := operatorClient(t, server, pool, "mailadmin")
	grantRole(t, pool, operatorUsername(t, pool, admin), "super_admin")
	adminEmail := operatorUsername(t, pool, admin) + "@example.test"

	// A reused database may hold a previous run's relay; the test starts from
	// what a fresh installation has.
	defaults, _ := json.Marshal(mail.DefaultValues())
	if _, err := pool.Exec(background, `UPDATE system_settings SET value=$1,encrypted_value=NULL,version=version+1 WHERE key='mail'`, defaults); err != nil {
		t.Fatalf("reset mail setting: %v", err)
	}
	dispatcherUnderTest.ReloadPolicy()

	// Fresh installation: off, and nothing queued by the orders other tests made.
	settings := admin.do(http.MethodGet, "/api/v1/admin/settings", nil, http.StatusOK)
	var mailSetting map[string]any
	for _, raw := range settings["items"].([]any) {
		if item := raw.(map[string]any); item["key"] == "mail" {
			mailSetting = item
		}
	}
	if mailSetting == nil {
		t.Fatal("mail 설정 행이 없습니다")
	}
	if value := mailSetting["value"].(map[string]any); value["enabled"] != false || value["smtp_port"] != float64(25) {
		t.Fatalf("mail must ship off on port 25: %v", value)
	}
	if mailSetting["is_secret"] != true || mailSetting["secret_configured"] != false {
		t.Fatalf("the password slot must be a secret that is not yet configured: %v", mailSetting)
	}
	admin.do(http.MethodPost, "/api/v1/admin/mail/test", map[string]any{}, http.StatusConflict)

	// Turning it on without a host is refused with the field named.
	version := mailSetting["version"]
	rejected := admin.do(http.MethodPut, "/api/v1/admin/settings/mail", map[string]any{"value": map[string]any{"enabled": true}, "version": version}, http.StatusBadRequest)
	if message := rejected["error"].(map[string]any)["message"]; !strings.Contains(message.(string), "mail.smtp_host") {
		t.Fatalf("refusal must name the field, got %v", message)
	}
	value := map[string]any{
		"enabled": true, "smtp_host": relay.Host(), "smtp_port": relay.Port(), "security": "auto", "skip_tls_verify": false,
		"username": "kkiit", "from_address": "kkiit@corp.example", "from_name": "Kkiit", "base_url": "https://kkiit.test/", "timeout_seconds": 5,
		"notify_order_paid": true, "notify_order_delivered": false, "notify_revision_requested": true,
	}
	admin.do(http.MethodPut, "/api/v1/admin/settings/mail", map[string]any{"value": value, "secret": "relay-secret", "version": version}, http.StatusOK)
	t.Cleanup(func() {
		_, _ = pool.Exec(background, `UPDATE system_settings SET value=value||'{"enabled":false}'::jsonb WHERE key='mail'`)
		dispatcherUnderTest.ReloadPolicy()
	})

	// The password is stored, reported as configured, and never returned.
	settings = admin.do(http.MethodGet, "/api/v1/admin/settings", nil, http.StatusOK)
	encoded, _ := json.Marshal(settings)
	if strings.Contains(string(encoded), "relay-secret") {
		t.Fatal("the SMTP password came back from the settings API")
	}
	for _, raw := range settings["items"].([]any) {
		if item := raw.(map[string]any); item["key"] == "mail" && item["secret_configured"] != true {
			t.Fatalf("password must show as configured: %v", item)
		}
	}

	// The test button sends a real message with the saved setting and records it.
	sent := admin.do(http.MethodPost, "/api/v1/admin/mail/test", map[string]any{}, http.StatusOK)
	if sent["sent"] != true || sent["recipient"] != adminEmail {
		t.Fatalf("test send=%v", sent)
	}
	if got := mailsTo(relay, adminEmail); len(got) != 1 || !strings.Contains(decodeSubject(t, got[0].Data), "SMTP 발송 테스트") {
		t.Fatalf("relay did not receive the test mail: %+v", got)
	}
	log := admin.do(http.MethodGet, "/api/v1/admin/mail/deliveries?status=sent", nil, http.StatusOK)
	if encoded, _ := json.Marshal(log); strings.Contains(string(encoded), "\"body\"") {
		t.Fatal("the delivery log must not carry bodies")
	}
	foundTest := false
	for _, raw := range log["items"].([]any) {
		if item := raw.(map[string]any); item["event_type"] == "test" && item["recipient"] == adminEmail {
			foundTest = true
		}
	}
	if !foundTest {
		t.Fatal("the test send is not in the delivery log")
	}

	// Event mail. The dispatcher caches settings, so it is told to look again.
	dispatcherUnderTest.ReloadPolicy()
	seller, talentID, sellerName := sellTalent(t, server, "mailseller", "메일 알림 상품", 70000)
	sellerEmail := sellerName + "@example.test"
	buyerName := uniqueName("mailbuyer")
	buyer := newClient(t, server.URL)
	buyer.register(buyerName)
	buyerEmail := buyerName + "@example.test"

	// Paying, delivering and asking for a revision in one go: the seller is
	// owed two mails (paid, revision) and the buyer none (delivered is off,
	// and the buyer did the paying). The two fold into one message.
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/revision", map[string]any{"details": "제목을 바꿔 주세요", "priority": "normal", "attachments": []any{}}, http.StatusCreated)

	eventually(t, "판매자에게 메일이 도착", 40*time.Second, func() bool { return len(mailsTo(relay, sellerEmail)) > 0 })
	// Anything still queued for the seller was folded in; give a straggler a
	// moment to prove it is not sent separately.
	time.Sleep(3 * time.Second)
	sellerMails := mailsTo(relay, sellerEmail)
	if len(sellerMails) != 1 {
		t.Fatalf("the seller must get one digest, got %d messages", len(sellerMails))
	}
	subject := decodeSubject(t, sellerMails[0].Data)
	if !strings.Contains(subject, "알림 2건") {
		t.Fatalf("digest subject=%q", subject)
	}
	body := sellerMails[0].Data
	for _, want := range []string{"■ 결제 완료", "■ 수정 요청", "https://kkiit.test/orders/" + orderID, "https://kkiit.test/profile/notifications"} {
		if !strings.Contains(body, want) {
			t.Fatalf("digest body lacks %q:\n%s", want, body)
		}
	}
	if got := mailsTo(relay, buyerEmail); len(got) != 0 {
		t.Fatalf("the buyer acted and has delivered switched off, yet got %d mails", len(got))
	}
	var queued, sellerSent int
	if err := pool.QueryRow(background, `SELECT count(*) FILTER (WHERE status IN ('queued','retry','sending')), count(*) FILTER (WHERE status='sent' AND recipient=$1 AND body IS NULL) FROM mail_deliveries`, sellerEmail).Scan(&queued, &sellerSent); err != nil {
		t.Fatalf("delivery rows: %v", err)
	}
	if queued != 0 || sellerSent != 2 {
		t.Fatalf("delivery rows: queued=%d seller sent without body=%d", queued, sellerSent)
	}

	// The relay goes away. The request still succeeds and the row says why.
	relay.Close()
	secondBuyer := newClient(t, server.URL)
	secondBuyer.register(uniqueName("mailbuyer2"))
	order := secondBuyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
	secondOrder, _ := order["id"].(string)
	started := time.Now()
	secondBuyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+secondOrder+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("pay")})
	if time.Since(started) > 3*time.Second {
		t.Fatalf("paying waited on the relay: %s", time.Since(started))
	}
	eventually(t, "죽은 릴레이가 발송 기록에 남음", 40*time.Second, func() bool {
		var lastError string
		err := pool.QueryRow(background, `SELECT COALESCE(last_error,'') FROM mail_deliveries WHERE recipient=$1 AND status='retry'`, sellerEmail).Scan(&lastError)
		return err == nil && strings.Contains(lastError, "SMTP 연결 실패")
	})
	failed := admin.do(http.MethodGet, "/api/v1/admin/mail/deliveries?status=retry", nil, http.StatusOK)
	if items := failed["items"].([]any); len(items) == 0 {
		t.Fatal("the failed attempt is not in the delivery log")
	}
}
