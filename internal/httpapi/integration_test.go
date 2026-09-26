package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hkjang/Kkiit/internal/cryptox"
	"github.com/hkjang/Kkiit/internal/database"
	"github.com/hkjang/Kkiit/internal/ui"
	"github.com/hkjang/Kkiit/internal/worker"
)

// These tests need a throwaway PostgreSQL. They create accounts and orders and
// do not clean up, so point KKIIT_TEST_DSN at a database you can drop.
//
//	make test-integration KKIIT_TEST_DSN=postgres://...
func integrationServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("KKIIT_TEST_DSN"))
	if dsn == "" {
		t.Skip("KKIIT_TEST_DSN이 없어 통합 테스트를 건너뜁니다.")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	box, err := cryptox.New(key)
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := &Server{DB: pool, Box: box, Version: "test", Logger: logger}
	dispatcherCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	dispatcher := &worker.Worker{DB: pool, Box: box, Logger: logger, Publish: api.PublishUser}
	api.Rescan = dispatcher.ScanNow
	api.SettingSaved = dispatcher.SettingSaved
	dispatcher.Maintenance = api.RunOrderMaintenance
	go dispatcher.Run(dispatcherCtx)
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	apiUnderTest = api
	dispatcherUnderTest = dispatcher
	return server, pool
}

// apiUnderTest exposes the server instance for the few tests that exercise
// process level behaviour rather than a request. Tests in this package run
// sequentially, so the last setup is the current one; a parallel test would
// need to build its own server instead of reading this.
var apiUnderTest *Server

// dispatcherUnderTest is the outbox worker behind apiUnderTest, for the tests
// that change a setting the worker caches.
var dispatcherUnderTest *worker.Worker

// grantRole promotes a freshly registered account. Operator accounts are
// normally seeded by an administrator, which the HTTP surface does not expose.
func grantRole(t *testing.T, pool *pgxpool.Pool, username, role string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO user_roles(user_id,role_code) SELECT id,$2 FROM users WHERE username=$1 ON CONFLICT DO NOTHING`, username, role); err != nil {
		t.Fatalf("grant %s: %v", role, err)
	}
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, base string) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &client{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
}

// raw returns the response itself, for the cases where the headers are the
// thing under test.
func (c *client) raw(method, path string) (*http.Response, error) {
	c.t.Helper()
	request, err := http.NewRequest(method, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	return c.http.Do(request)
}

func (c *client) do(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	return c.doWithHeaders(method, path, body, want, nil)
}

func (c *client) doWithHeaders(method, path string, body any, want int, headers map[string]string) map[string]any {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encode %s %s: %v", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		c.t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, want, payload)
	}
	if len(payload) == 0 {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		c.t.Fatalf("decode %s %s: %v body=%s", method, path, err, payload)
	}
	return decoded
}

func uniqueName(prefix string) string {
	return uniqueNameAt(prefix, time.Now().UnixNano())
}

// uniqueNameAt appends exactly nine digits so the result has a length callers
// can count on; an unpadded suffix let a fixed slice go out of range, and a
// panic in one test takes the whole package's binary with it.
func uniqueNameAt(prefix string, nanos int64) string {
	return fmt.Sprintf("%s%09d", prefix, nanos%1_000_000_000)
}

func (c *client) register(name string) {
	c.t.Helper()
	c.do(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": name, "email": name + "@example.test", "display_name": name, "password": "IntegrationPass!23",
	}, http.StatusCreated)
}

// eventually keeps checking because the dispatcher runs out of band; a fixed
// sleep would either be flaky or slow.
func eventually(t *testing.T, what string, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s: 제한 시간 안에 발생하지 않았습니다", what)
}

func TestIntegrationOutboxDeliversNotificationsAndSignedWebhooks(t *testing.T) {
	server, _ := integrationServer(t)

	var mu sync.Mutex
	var deliveries []struct {
		Signature string
		Timestamp string
		Event     string
		Body      []byte
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		deliveries = append(deliveries, struct {
			Signature string
			Timestamp string
			Event     string
			Body      []byte
		}{r.Header.Get("X-Kkiit-Signature"), r.Header.Get("X-Kkiit-Timestamp"), r.Header.Get("X-Kkiit-Event"), body})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	sellerName, buyerName := uniqueName("seller"), uniqueName("buyer")
	seller := newClient(t, server.URL)
	seller.register(sellerName)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "통합 테스트 판매자", "biography": "", "skills": []string{"go"}, "capacity": 5, "settings": map[string]any{},
	}, http.StatusOK)

	hook := seller.do(http.MethodPost, "/api/v1/me/webhooks", map[string]any{
		"name": "통합 테스트 훅", "target_url": receiver.URL + "/hook", "events": []string{"*"},
	}, http.StatusCreated)
	secret, _ := hook["secret"].(string)
	if secret == "" {
		t.Fatal("웹훅 서명 키가 한 번만 반환되어야 합니다")
	}

	talent := seller.do(http.MethodPost, "/api/v1/talents", map[string]any{
		"title": "통합 테스트 상품", "summary": "이벤트 백본 검증", "description": "이벤트 전달을 검증하는 상품입니다.",
		"service_type": "HUMAN", "base_price": 100000, "delivery_days": 3, "currency": "KRW", "revision_count": 1,
		"scope_included": []string{}, "scope_excluded": []string{}, "deliverables": []string{}, "tags": []string{"test"},
		"faq": []any{}, "refund_policy": "전액 환불", "instant_order": true, "quote_required": false, "subscription_enabled": false,
		"packages":     []map[string]any{{"package_type": "BASIC", "name": "기본", "description": "기본", "price": 100000, "delivery_days": 3, "revision_count": 1, "features": []string{}, "deliverables": []string{}, "sort_order": 0, "active": true}},
		"requirements": []map[string]any{{"label": "요구사항", "help_text": "", "field_type": "textarea", "required": true, "options": []any{}, "validation": map[string]any{}, "sort_order": 0}},
	}, http.StatusCreated)
	talentID, _ := talent["id"].(string)
	if talentID == "" {
		t.Fatal("상품 식별자가 없습니다")
	}
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)

	buyer := newClient(t, server.URL)
	buyer.register(buyerName)
	order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
	orderID, _ := order["id"].(string)

	// The seller is the counterparty, so the order event must reach their inbox
	// while the buyer, who caused it, must not be notified about their own act.
	eventually(t, "판매자 알림 도착", 20*time.Second, func() bool {
		inbox := seller.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
		items, _ := inbox["items"].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			if item["event_type"] == "OrderCreated" && item["link"] == "/orders/"+orderID {
				if subject, _ := item["subject"].(string); !strings.Contains(subject, "새 주문") {
					t.Fatalf("알림 제목이 템플릿으로 렌더링되지 않았습니다: %v", item["subject"])
				}
				return true
			}
		}
		return false
	})
	buyerInbox := buyer.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
	if items, _ := buyerInbox["items"].([]any); len(items) != 0 {
		t.Fatalf("행위자 본인에게 알림이 생성되었습니다: %v", items)
	}

	eventually(t, "웹훅 전달", 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(deliveries) > 0
	})
	mu.Lock()
	received := deliveries[0]
	mu.Unlock()
	if received.Event == "" {
		t.Fatal("X-Kkiit-Event 헤더가 없습니다")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(received.Timestamp))
	mac.Write([]byte("."))
	mac.Write(received.Body)
	expected := "t=" + received.Timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
	if received.Signature != expected {
		t.Fatalf("서명 불일치\n got=%s\nwant=%s", received.Signature, expected)
	}
	var envelope map[string]any
	if err := json.Unmarshal(received.Body, &envelope); err != nil {
		t.Fatalf("본문 디코딩: %v", err)
	}
	for _, field := range []string{"delivery_id", "event_id", "event_type", "aggregate_type", "aggregate_id", "occurred_at", "payload"} {
		if _, ok := envelope[field]; !ok {
			t.Fatalf("전달 본문에 %s가 없습니다: %s", field, received.Body)
		}
	}

	// The test button must travel the same signing and retry path.
	seller.do(http.MethodPost, "/api/v1/me/webhooks/"+fmt.Sprint(hook["id"])+"/test", nil, http.StatusAccepted)
	eventually(t, "테스트 이벤트 전달", 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, item := range deliveries {
			if item.Event == "WebhookTest" {
				return true
			}
		}
		return false
	})
	history := seller.do(http.MethodGet, "/api/v1/me/webhooks/"+fmt.Sprint(hook["id"])+"/deliveries", nil, http.StatusOK)
	if items, _ := history["items"].([]any); len(items) < 2 {
		t.Fatalf("전달 이력이 기록되지 않았습니다: %v", items)
	}

	// A second dispatch pass must not duplicate an already delivered event.
	before := seller.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
	beforeCount := len(before["items"].([]any))
	if _, err := server.Client().Get(server.URL + "/health/live"); err != nil {
		t.Fatalf("health: %v", err)
	}
	time.Sleep(3 * time.Second)
	after := seller.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
	if got := len(after["items"].([]any)); got != beforeCount {
		t.Fatalf("알림이 중복 생성되었습니다: %d -> %d", beforeCount, got)
	}
}

func TestIntegrationNotificationPreferenceSuppressesInbox(t *testing.T) {
	server, _ := integrationServer(t)
	sellerName, buyerName := uniqueName("mute"), uniqueName("mutebuyer")
	seller := newClient(t, server.URL)
	seller.register(sellerName)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "알림 끄기 검증", "biography": "", "skills": []string{"go"}, "capacity": 5, "settings": map[string]any{},
	}, http.StatusOK)
	seller.do(http.MethodPut, "/api/v1/me/notification-preferences", map[string]any{
		"items": []map[string]any{{"event_type": "OrderCreated", "channels": []string{"web"}, "enabled": false}},
	}, http.StatusOK)

	talent := seller.do(http.MethodPost, "/api/v1/talents", map[string]any{
		"title": "알림 끄기 상품", "summary": "무음", "description": "알림 수신 설정을 검증하는 상품입니다.",
		"service_type": "HUMAN", "base_price": 50000, "delivery_days": 2, "currency": "KRW", "revision_count": 1,
		"scope_included": []string{}, "scope_excluded": []string{}, "deliverables": []string{}, "tags": []string{"test"},
		"faq": []any{}, "refund_policy": "전액 환불", "instant_order": true, "quote_required": false, "subscription_enabled": false,
		"packages":     []map[string]any{{"package_type": "BASIC", "name": "기본", "description": "기본", "price": 50000, "delivery_days": 2, "revision_count": 1, "features": []string{}, "deliverables": []string{}, "sort_order": 0, "active": true}},
		"requirements": []map[string]any{{"label": "요구사항", "help_text": "", "field_type": "textarea", "required": true, "options": []any{}, "validation": map[string]any{}, "sort_order": 0}},
	}, http.StatusCreated)
	talentID, _ := talent["id"].(string)
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)

	buyer := newClient(t, server.URL)
	buyer.register(buyerName)
	buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)

	time.Sleep(5 * time.Second)
	inbox := seller.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
	items, _ := inbox["items"].([]any)
	for _, raw := range items {
		if item, _ := raw.(map[string]any); item["event_type"] == "OrderCreated" {
			t.Fatal("끈 이벤트로 알림이 생성되었습니다")
		}
	}
}

// sellTalent registers a seller with one published talent and returns the
// seller client and the talent identifier.
func sellTalent(t *testing.T, server *httptest.Server, prefix, title string, price int64) (*client, string, string) {
	t.Helper()
	name := uniqueName(prefix)
	seller := newClient(t, server.URL)
	seller.register(name)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": title, "biography": "", "skills": []string{"go"}, "capacity": 5, "settings": map[string]any{},
	}, http.StatusOK)
	talent := seller.do(http.MethodPost, "/api/v1/talents", map[string]any{
		"title": title, "summary": title, "description": title + " 상세 설명입니다.",
		"service_type": "HUMAN", "base_price": price, "delivery_days": 3, "currency": "KRW", "revision_count": 1,
		"scope_included": []string{}, "scope_excluded": []string{}, "deliverables": []string{}, "tags": []string{"test"},
		"faq": []any{}, "refund_policy": "전액 환불", "instant_order": true, "quote_required": false, "subscription_enabled": false,
		"packages":     []map[string]any{{"package_type": "BASIC", "name": "기본", "description": "기본", "price": price, "delivery_days": 3, "revision_count": 1, "features": []string{}, "deliverables": []string{}, "sort_order": 0, "active": true}},
		"requirements": []map[string]any{{"label": "요구사항", "help_text": "", "field_type": "textarea", "required": true, "options": []any{}, "validation": map[string]any{}, "sort_order": 0}},
	}, http.StatusCreated)
	talentID, _ := talent["id"].(string)
	if talentID == "" {
		t.Fatal("상품 식별자가 없습니다")
	}
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)
	return seller, talentID, name
}

func payAndDeliver(t *testing.T, buyer, seller *client, talentID string) string {
	t.Helper()
	order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
	orderID, _ := order["id"].(string)
	buyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+orderID+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("pay")})
	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "IN_PROGRESS", "note": ""}, http.StatusOK)
	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/deliveries", map[string]any{"delivery_type": "text", "content": map[string]any{"text": "결과물"}, "description": "납품"}, http.StatusCreated)
	return orderID
}

// ledgerBalance returns debits minus credits per account plus the overall sum,
// which must be zero for a correctly booked order.
func ledgerBalance(t *testing.T, pool *pgxpool.Pool, orderID string) (map[string]int64, int64) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT account,
		COALESCE(sum(CASE WHEN direction='debit' THEN amount ELSE -amount END),0)
		FROM ledger_entries WHERE order_id=$1 GROUP BY account`, orderID)
	if err != nil {
		t.Fatalf("ledger query: %v", err)
	}
	defer rows.Close()
	balances := map[string]int64{}
	var total int64
	for rows.Next() {
		var account string
		var value int64
		if rows.Scan(&account, &value) == nil {
			balances[account] = value
			total += value
		}
	}
	return balances, total
}

func TestIntegrationPartialRefundKeepsLedgerBalanced(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "dispseller", "분쟁 부분환불 상품", 100_000)
	buyerName := uniqueName("dispbuyer")
	buyer := newClient(t, server.URL)
	buyer.register(buyerName)
	orderID := payAndDeliver(t, buyer, seller, talentID)

	// Confirming the purchase books the settlement, so the dispute has to
	// reverse real ledger entries rather than only touching escrow.
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "결과물이 요구사항과 다릅니다.", "evidence": []any{}}, http.StatusCreated)

	operatorName := uniqueName("operator")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "operator")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	queue := operator.do(http.MethodGet, "/api/v1/admin/disputes?state=open", nil, http.StatusOK)
	items, _ := queue["items"].([]any)
	var disputeID string
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["order_id"] == orderID {
			disputeID, _ = item["id"].(string)
		}
	}
	if disputeID == "" {
		t.Fatal("분쟁이 운영 대기열에 나타나지 않았습니다")
	}
	result := operator.do(http.MethodPost, "/api/v1/admin/disputes/"+disputeID+"/resolve", map[string]any{"outcome": "refund_partial", "refund_amount": 40000, "note": "절반만 인정"}, http.StatusOK)
	if result["order_state"] != "COMPLETED" {
		t.Fatalf("부분 환불 후 주문 상태=%v", result["order_state"])
	}

	balances, total := ledgerBalance(t, pool, orderID)
	if total != 0 {
		t.Fatalf("원장이 맞지 않습니다: %v (합계 %d)", balances, total)
	}
	if balances["Escrow"] != 0 {
		t.Fatalf("에스크로 잔액이 남았습니다: %d", balances["Escrow"])
	}
	if balances["Buyer Refund"] != -40000 {
		t.Fatalf("환불 계정 잔액=%d", balances["Buyer Refund"])
	}
	// 60,000 remains for the seller at the default 10% platform fee.
	if balances["Platform Revenue"] != -6000 || balances["Seller Payable"] != -54000 {
		t.Fatalf("잔여 정산 배분이 잘못되었습니다: %v", balances)
	}
}

func TestIntegrationFullRefundClosesOrderAndSettlement(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "refundseller", "분쟁 전액환불 상품", 70_000)
	buyerName := uniqueName("refundbuyer")
	buyer := newClient(t, server.URL)
	buyer.register(buyerName)
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "납품물을 사용할 수 없습니다.", "evidence": []any{}}, http.StatusCreated)

	// A second dispute on the same order must not open while one is pending.
	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "구매자 주장에 동의하지 않습니다.", "evidence": []any{}}, http.StatusConflict)

	operatorName := uniqueName("refundop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "operator")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)
	queue := operator.do(http.MethodGet, "/api/v1/admin/disputes?state=open", nil, http.StatusOK)
	var disputeID string
	for _, raw := range queue["items"].([]any) {
		item, _ := raw.(map[string]any)
		if item["order_id"] == orderID {
			disputeID, _ = item["id"].(string)
		}
	}
	result := operator.do(http.MethodPost, "/api/v1/admin/disputes/"+disputeID+"/resolve", map[string]any{"outcome": "refund_full", "note": "전액 환불"}, http.StatusOK)
	if result["order_state"] != "REFUNDED" {
		t.Fatalf("전액 환불 후 주문 상태=%v", result["order_state"])
	}
	balances, total := ledgerBalance(t, pool, orderID)
	if total != 0 || balances["Escrow"] != 0 {
		t.Fatalf("원장이 맞지 않습니다: %v (합계 %d)", balances, total)
	}
	if balances["Seller Payable"] != 0 {
		t.Fatalf("전액 환불인데 판매자 지급이 남았습니다: %d", balances["Seller Payable"])
	}
	var settlements int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM settlements WHERE order_id=$1 AND state<>'cancelled'`, orderID).Scan(&settlements); err != nil {
		t.Fatalf("settlement query: %v", err)
	}
	if settlements != 0 {
		t.Fatalf("전액 환불 후 살아있는 정산이 %d건 남았습니다", settlements)
	}
	// Both parties must learn how the dispute ended.
	eventually(t, "분쟁 처리 알림", 20*time.Second, func() bool {
		inbox := buyer.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
		for _, raw := range inbox["items"].([]any) {
			if item, _ := raw.(map[string]any); item["event_type"] == "DisputeResolved" {
				return true
			}
		}
		return false
	})
}

func operatorClient(t *testing.T, server *httptest.Server, pool *pgxpool.Pool, prefix string) *client {
	t.Helper()
	name := uniqueName(prefix)
	operator := newClient(t, server.URL)
	operator.register(name)
	grantRole(t, pool, name, "operator")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23"}, http.StatusOK)
	return operator
}

func TestIntegrationRiskScanHoldsDueSettlement(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "riskseller", "위험 평가 상품", 2_000_000)
	buyerName := uniqueName("riskbuyer")
	buyer := newClient(t, server.URL)
	buyer.register(buyerName)

	// A brand new account placing several large orders with a seller that has
	// never completed one is exactly the shape the rules are meant to surface.
	var orderID string
	for i := 0; i < 5; i++ {
		order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
		orderID, _ = order["id"].(string)
	}
	buyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+orderID+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("pay")})
	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "IN_PROGRESS", "note": ""}, http.StatusOK)
	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/deliveries", map[string]any{"delivery_type": "text", "content": map[string]any{"text": "결과물"}, "description": "납품"}, http.StatusCreated)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)

	operator := operatorClient(t, server, pool, "riskop")
	operator.do(http.MethodPost, "/api/v1/admin/risk/rescan", map[string]any{}, http.StatusOK)

	queue := operator.do(http.MethodGet, "/api/v1/admin/risk", nil, http.StatusOK)
	var level string
	var actions []any
	for _, raw := range queue["items"].([]any) {
		item, _ := raw.(map[string]any)
		if item["resource_id"] == orderID {
			level, _ = item["level"].(string)
			actions, _ = item["actions"].([]any)
			if item["order_number"] == nil {
				t.Fatal("위험 항목에 주문 정보가 붙지 않았습니다")
			}
		}
	}
	if level != "HIGH" && level != "CRITICAL" {
		t.Fatalf("고위험 주문 등급=%q", level)
	}
	if len(actions) == 0 {
		t.Fatal("정산 보류 조치가 없습니다")
	}

	// Bring the settlement's scheduled date forward instead of waiting days.
	if _, err := pool.Exec(context.Background(), `UPDATE settlements SET scheduled_at=now()-interval '1 minute' WHERE order_id=$1`, orderID); err != nil {
		t.Fatalf("settlement schedule: %v", err)
	}
	operator.do(http.MethodPost, "/api/v1/admin/risk/rescan", map[string]any{}, http.StatusOK)

	var state string
	var reason *string
	if err := pool.QueryRow(context.Background(), `SELECT state,hold_reason FROM settlements WHERE order_id=$1`, orderID).Scan(&state, &reason); err != nil {
		t.Fatalf("settlement query: %v", err)
	}
	if state != "hold" || reason == nil || !strings.Contains(*reason, "위험 등급") {
		t.Fatalf("정산 상태=%s 사유=%v", state, reason)
	}
	eventually(t, "정산 보류 알림", 20*time.Second, func() bool {
		inbox := seller.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
		for _, raw := range inbox["items"].([]any) {
			if item, _ := raw.(map[string]any); item["event_type"] == "SettlementHeld" {
				return true
			}
		}
		return false
	})
}

func TestIntegrationLowRiskSettlementStaysScheduledWithoutBatch(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "calmseller", "저위험 상품", 50_000)
	buyerName := uniqueName("calmbuyer")
	buyer := newClient(t, server.URL)
	buyer.register(buyerName)
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)
	if _, err := pool.Exec(context.Background(), `UPDATE settlements SET scheduled_at=now()-interval '1 minute' WHERE order_id=$1`, orderID); err != nil {
		t.Fatalf("settlement schedule: %v", err)
	}
	operator := operatorClient(t, server, pool, "calmop")
	operator.do(http.MethodPost, "/api/v1/admin/risk/rescan", map[string]any{}, http.StatusOK)

	// batch_enabled defaults to false, so a healthy settlement waits for an
	// operator instead of advancing on its own.
	var state string
	if err := pool.QueryRow(context.Background(), `SELECT state FROM settlements WHERE order_id=$1`, orderID).Scan(&state); err != nil {
		t.Fatalf("settlement query: %v", err)
	}
	if state != "scheduled" {
		t.Fatalf("자동 배치가 꺼져 있으면 예정 상태를 유지해야 합니다: %s", state)
	}
}

func createTalent(t *testing.T, seller *client, title, description string, price int64, packages int, faq []any) string {
	t.Helper()
	built := make([]map[string]any, 0, packages)
	for i := 0; i < packages; i++ {
		built = append(built, map[string]any{"package_type": []string{"BASIC", "STANDARD", "PREMIUM"}[i%3], "name": "패키지", "description": "설명",
			"price": price * int64(i+1), "delivery_days": 3 + i, "revision_count": 1, "features": []string{}, "deliverables": []string{}, "sort_order": i, "active": true})
	}
	talent := seller.do(http.MethodPost, "/api/v1/talents", map[string]any{
		"title": title, "summary": title, "description": description,
		"service_type": "HUMAN", "base_price": price, "delivery_days": 3, "currency": "KRW", "revision_count": 1,
		"scope_included": []string{}, "scope_excluded": []string{}, "deliverables": []string{}, "tags": []string{"test"},
		"faq": faq, "refund_policy": "전액 환불", "instant_order": true, "quote_required": false, "subscription_enabled": false,
		"packages":     built,
		"requirements": []map[string]any{{"label": "요구사항", "help_text": "", "field_type": "textarea", "required": true, "options": []any{}, "validation": map[string]any{}, "sort_order": 0}},
	}, http.StatusCreated)
	id, _ := talent["id"].(string)
	if id == "" {
		t.Fatal("상품 식별자가 없습니다")
	}
	seller.do(http.MethodPost, "/api/v1/talents/"+id+"/publish", nil, http.StatusOK)
	return id
}

func registerSeller(t *testing.T, server *httptest.Server, prefix string) *client {
	t.Helper()
	seller := newClient(t, server.URL)
	seller.register(uniqueName(prefix))
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "판매자", "biography": "", "skills": []string{"go"}, "capacity": 5, "settings": map[string]any{},
	}, http.StatusOK)
	return seller
}

func TestIntegrationTrustScoringRanksProvenSellersFirst(t *testing.T) {
	server, pool := integrationServer(t)
	keyword := uniqueName("랭킹검증")

	proven := registerSeller(t, server, "provenseller")
	provenTalent := createTalent(t, proven, keyword+" 전문 서비스", strings.Repeat("이 서비스는 요구사항을 자세히 분석하고 결과물을 제공합니다. ", 12), 100_000, 3,
		[]any{map[string]any{"question": "얼마나 걸리나요?", "answer": "3일입니다."}})

	thin := registerSeller(t, server, "thinseller")
	thinTalent := createTalent(t, thin, keyword+" 간단 서비스", "짧은 설명입니다.", 100_000, 1, []any{})

	// One delivered and reviewed order is the difference between the two.
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("trustbuyer"))
	orderID := payAndDeliver(t, buyer, proven, provenTalent)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/review", map[string]any{
		"quality": 5, "communication": 5, "timeliness": 5, "professionalism": 5, "repurchase": true, "body": "훌륭했습니다.",
	}, http.StatusCreated)

	operator := operatorClient(t, server, pool, "trustop")
	operator.do(http.MethodPost, "/api/v1/admin/risk/rescan", map[string]any{}, http.StatusOK)

	var provenScore, thinScore float64
	if err := pool.QueryRow(context.Background(), `SELECT quality_score FROM talents WHERE id=$1`, provenTalent).Scan(&provenScore); err != nil {
		t.Fatalf("quality query: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT quality_score FROM talents WHERE id=$1`, thinTalent).Scan(&thinScore); err != nil {
		t.Fatalf("quality query: %v", err)
	}
	if provenScore <= thinScore {
		t.Fatalf("실적 있는 상품 품질=%v 빈약한 상품 품질=%v", provenScore, thinScore)
	}

	search := buyer.do(http.MethodGet, "/api/v1/talents?q="+keyword, nil, http.StatusOK)
	items, _ := search["items"].([]any)
	if len(items) < 2 {
		t.Fatalf("검색 결과가 %d건입니다", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["id"] != provenTalent {
		t.Fatalf("검색 1위=%v, 실적 있는 상품이 앞서야 합니다", first["id"])
	}
	seller, _ := first["seller"].(map[string]any)
	if seller["level"] == nil || seller["rating"] == nil {
		t.Fatalf("판매자 신뢰 정보가 응답에 없습니다: %v", seller)
	}
	if rating, _ := seller["rating"].(float64); rating < 4.9 {
		t.Fatalf("판매자 평점=%v", seller["rating"])
	}

	// Recommendations must explain themselves rather than return a bare number.
	recommended := buyer.do(http.MethodGet, "/api/v1/recommendations?q="+keyword, nil, http.StatusOK)
	list, _ := recommended["items"].([]any)
	if len(list) == 0 {
		t.Fatal("추천 결과가 없습니다")
	}
	top, _ := list[0].(map[string]any)
	if top["explanation"] == "" || top["components"] == nil {
		t.Fatalf("추천 설명이 없습니다: %v", top)
	}
	if recommended["algorithm_version"] != "ranking_v2" {
		t.Fatalf("알고리즘 버전=%v", recommended["algorithm_version"])
	}
}

func TestIntegrationFavoritesAndPortfolioAreVisibleWhereTheyMatter(t *testing.T) {
	server, _ := integrationServer(t)
	keyword := uniqueName("찜검증")
	seller := registerSeller(t, server, "favseller")
	talentID := createTalent(t, seller, keyword+" 서비스", "이 서비스는 상세한 설명을 제공합니다.", 80_000, 2, []any{})

	// Portfolio media is uploaded as a public file so a visitor with no session
	// can still see the seller's work on the product page.
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "work.png")
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if _, err := part.Write([]byte("\x89PNG\r\n\x1a\n작업 이미지 데이터")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	writer.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/me/files", &upload)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := seller.http.Do(request)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("업로드 상태=%d 본문=%s", response.StatusCode, body)
	}
	var uploaded map[string]any
	if err := json.Unmarshal(body, &uploaded); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	mediaURL, _ := uploaded["download_url"].(string)
	seller.do(http.MethodPost, "/api/v1/me/portfolios", map[string]any{
		"title": "지난 브랜드 작업", "description": "리뉴얼 사례입니다.", "tags": []string{"브랜딩"},
		"media": []map[string]any{{"url": mediaURL, "name": "work.png", "mime_type": "image/png"}},
	}, http.StatusCreated)

	// An anonymous visitor sees the portfolio and can load its media.
	anonymous := newClient(t, server.URL)
	profile := anonymous.do(http.MethodGet, "/api/v1/talents/"+talentID, nil, http.StatusOK)
	sellerInfo, _ := profile["seller"].(map[string]any)
	sellerID, _ := sellerInfo["id"].(string)
	public := anonymous.do(http.MethodGet, "/api/v1/sellers/"+sellerID+"/portfolios", nil, http.StatusOK)
	if items, _ := public["items"].([]any); len(items) != 1 {
		t.Fatalf("공개 포트폴리오 %d건", len(items))
	}
	mediaResponse, err := anonymous.http.Get(server.URL + mediaURL)
	if err != nil {
		t.Fatalf("media fetch: %v", err)
	}
	mediaResponse.Body.Close()
	if mediaResponse.StatusCode != http.StatusOK {
		t.Fatalf("비로그인 미디어 응답=%d", mediaResponse.StatusCode)
	}

	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("favbuyer"))
	result := buyer.do(http.MethodPost, "/api/v1/talents/"+talentID+"/favorite", nil, http.StatusOK)
	if result["favorited"] != true || result["favorite_count"] != float64(1) {
		t.Fatalf("찜 결과=%v", result)
	}
	// Repeating the call must stay idempotent rather than double counting.
	again := buyer.do(http.MethodPost, "/api/v1/talents/"+talentID+"/favorite", nil, http.StatusOK)
	if again["favorite_count"] != float64(1) {
		t.Fatalf("중복 찜 후 개수=%v", again["favorite_count"])
	}

	saved := buyer.do(http.MethodGet, "/api/v1/me/favorites", nil, http.StatusOK)
	items, _ := saved["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("찜 목록 %d건", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["id"] != talentID || first["favorited"] != true {
		t.Fatalf("찜 목록 항목=%v", first)
	}

	search := buyer.do(http.MethodGet, "/api/v1/talents?q="+keyword, nil, http.StatusOK)
	found, _ := search["items"].([]any)
	if len(found) == 0 {
		t.Fatal("검색 결과가 없습니다")
	}
	entry, _ := found[0].(map[string]any)
	if entry["favorited"] != true || entry["favorite_count"] != float64(1) {
		t.Fatalf("검색 결과의 찜 상태=%v", entry)
	}

	// Another visitor must not inherit the buyer's favourite state.
	other := newClient(t, server.URL)
	other.register(uniqueName("favother"))
	otherSearch := other.do(http.MethodGet, "/api/v1/talents?q="+keyword, nil, http.StatusOK)
	otherEntry, _ := otherSearch["items"].([]any)[0].(map[string]any)
	if otherEntry["favorited"] != false {
		t.Fatalf("다른 사용자의 찜 상태=%v", otherEntry["favorited"])
	}

	buyer.do(http.MethodDelete, "/api/v1/talents/"+talentID+"/favorite", nil, http.StatusOK)
	empty := buyer.do(http.MethodGet, "/api/v1/me/favorites", nil, http.StatusOK)
	if list, _ := empty["items"].([]any); len(list) != 0 {
		t.Fatalf("해제 후 찜 목록 %d건", len(list))
	}
}

func TestIntegrationCouponDiscountsBuyerWithoutTouchingSellerPayout(t *testing.T) {
	server, pool := integrationServer(t)
	operator := operatorClient(t, server, pool, "couponop")
	code := strings.ToUpper(uniqueName("SAVE"))
	operator.do(http.MethodPost, "/api/v1/admin/coupons", map[string]any{
		"code": code, "name": "통합 테스트 할인", "discount_type": "percent", "discount_value": 20,
		"min_order_amount": 10_000, "per_user_limit": 1, "active": true,
	}, http.StatusCreated)

	seller, talentID, _ := sellTalent(t, server, "couponseller", "쿠폰 검증 상품", 100_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("couponbuyer"))

	preview := buyer.do(http.MethodPost, "/api/v1/coupons/preview", map[string]any{"code": code, "talent_id": talentID}, http.StatusOK)
	if preview["discount_amount"] != float64(20_000) || preview["payable_amount"] != float64(80_000) {
		t.Fatalf("미리보기=%v", preview)
	}

	order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}, "coupon_code": strings.ToLower(code),
	}, http.StatusCreated)
	orderID, _ := order["id"].(string)
	if order["amount"] != float64(100_000) || order["discount_amount"] != float64(20_000) || order["payable_amount"] != float64(80_000) {
		t.Fatalf("주문 금액=%v", order)
	}

	// A one-per-user coupon must be refused on the second attempt.
	buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}, "coupon_code": code,
	}, http.StatusConflict)

	payment := buyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+orderID+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("pay")})
	if payment["amount"] != float64(80_000) || payment["discount_amount"] != float64(20_000) {
		t.Fatalf("결제 응답=%v", payment)
	}
	balances, total := ledgerBalance(t, pool, orderID)
	if total != 0 {
		t.Fatalf("결제 후 원장 합계=%d (%v)", total, balances)
	}
	if balances["Buyer Payment"] != 80_000 || balances["Promotion Expense"] != 20_000 || balances["Escrow"] != -100_000 {
		t.Fatalf("결제 원장=%v", balances)
	}

	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "IN_PROGRESS", "note": ""}, http.StatusOK)
	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/deliveries", map[string]any{"delivery_type": "text", "content": map[string]any{"text": "결과물"}, "description": "납품"}, http.StatusCreated)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)

	// The seller settles on the full order value; the platform absorbed the coupon.
	var gross, net int64
	if err := pool.QueryRow(context.Background(), `SELECT gross_amount,net_amount FROM settlements WHERE order_id=$1`, orderID).Scan(&gross, &net); err != nil {
		t.Fatalf("settlement query: %v", err)
	}
	if gross != 100_000 || net != 90_000 {
		t.Fatalf("정산 총액=%d 순액=%d", gross, net)
	}
	if _, total := ledgerBalance(t, pool, orderID); total != 0 {
		t.Fatalf("구매확정 후 원장 합계=%d", total)
	}

	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "결과물이 요구사항과 다릅니다.", "evidence": []any{}}, http.StatusCreated)
	queue := operator.do(http.MethodGet, "/api/v1/admin/disputes?state=open", nil, http.StatusOK)
	var disputeID string
	for _, raw := range queue["items"].([]any) {
		if item, _ := raw.(map[string]any); item["order_id"] == orderID {
			disputeID, _ = item["id"].(string)
		}
	}
	result := operator.do(http.MethodPost, "/api/v1/admin/disputes/"+disputeID+"/resolve", map[string]any{"outcome": "refund_full", "note": "전액 환불"}, http.StatusOK)
	// The buyer only paid 80,000, so that is all that returns to them.
	if result["buyer_refund_amount"] != float64(80_000) || result["promotion_refund_amount"] != float64(20_000) {
		t.Fatalf("환불 배분=%v", result)
	}
	balances, total = ledgerBalance(t, pool, orderID)
	if total != 0 || balances["Escrow"] != 0 || balances["Seller Payable"] != 0 {
		t.Fatalf("환불 후 원장=%v (합계 %d)", balances, total)
	}
	if balances["Buyer Refund"] != -80_000 || balances["Promotion Recovery"] != -20_000 {
		t.Fatalf("환불 계정 잔액=%v", balances)
	}
	var refunded int64
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(sum(amount),0) FROM refunds WHERE order_id=$1`, orderID).Scan(&refunded); err != nil {
		t.Fatalf("refund query: %v", err)
	}
	if refunded != 80_000 {
		t.Fatalf("환불 기록 금액=%d", refunded)
	}

	usage := operator.do(http.MethodGet, "/api/v1/admin/coupons", nil, http.StatusOK)
	for _, raw := range usage["items"].([]any) {
		if item, _ := raw.(map[string]any); item["code"] == code {
			if item["redemption_count"] != float64(1) || item["redeemed_amount"] != float64(20_000) {
				t.Fatalf("쿠폰 사용 통계=%v", item)
			}
		}
	}
}

// An operator who retypes an existing code needs to be told the code is taken.
// Reporting "쿠폰을 찾을 수 없습니다" for a coupon they are looking at leaves them
// with no way to find out what is actually wrong.
func TestIntegrationDuplicateCouponCodeOnUpdateReportsConflictNotMissing(t *testing.T) {
	server, pool := integrationServer(t)
	operator := operatorClient(t, server, pool, "coupondupop")

	takenCode := strings.ToUpper(uniqueName("TAKEN"))
	ownCode := strings.ToUpper(uniqueName("OWN"))
	newCoupon := func(code string) string {
		created := operator.do(http.MethodPost, "/api/v1/admin/coupons", map[string]any{
			"code": code, "name": "중복 검증 " + code, "discount_type": "fixed", "discount_value": 5_000,
			"min_order_amount": 10_000, "per_user_limit": 1, "active": true,
		}, http.StatusCreated)
		id, _ := created["id"].(string)
		if id == "" {
			t.Fatalf("쿠폰 %s 생성 응답에 id가 없습니다: %v", code, created)
		}
		return id
	}
	newCoupon(takenCode)
	ownID := newCoupon(ownCode)

	// Renaming the second coupon onto the first one's code is a unique
	// constraint violation, not a missing row.
	conflict := operator.do(http.MethodPut, "/api/v1/admin/coupons/"+ownID, map[string]any{
		"code": takenCode, "name": "중복으로 바꾸기", "discount_type": "fixed", "discount_value": 5_000,
		"min_order_amount": 10_000, "per_user_limit": 1, "active": true,
	}, http.StatusConflict)
	failure, _ := conflict["error"].(map[string]any)
	if failure["code"] != "coupon_exists" {
		t.Fatalf("중복 코드 수정 오류=%v", conflict)
	}

	// A coupon that really is absent must still read as absent.
	missing := operator.do(http.MethodPut, "/api/v1/admin/coupons/"+uuid.New().String(), map[string]any{
		"code": strings.ToUpper(uniqueName("GONE")), "name": "없는 쿠폰", "discount_type": "fixed", "discount_value": 5_000,
		"min_order_amount": 10_000, "per_user_limit": 1, "active": true,
	}, http.StatusNotFound)
	if absent, _ := missing["error"].(map[string]any); absent["code"] != "coupon_not_found" {
		t.Fatalf("없는 쿠폰 수정 오류=%v", missing)
	}

	// The refused update must not have partially landed.
	listed := operator.do(http.MethodGet, "/api/v1/admin/coupons", nil, http.StatusOK)
	var found map[string]any
	for _, raw := range listed["items"].([]any) {
		if item, _ := raw.(map[string]any); item["id"] == ownID {
			found = item
		}
	}
	if found == nil {
		t.Fatalf("수정을 거절당한 쿠폰 %s이 목록에서 사라졌습니다", ownID)
	}
	if found["code"] != ownCode || found["name"] != "중복 검증 "+ownCode {
		t.Fatalf("거절된 수정이 쿠폰을 바꿨습니다: %v", found)
	}

	// The original code is still usable, so the duplicate never took effect.
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("coupondupbuyer"))
	_, talentID, _ := sellTalent(t, server, "coupondupseller", "중복 코드 검증 상품", 100_000)
	preview := buyer.do(http.MethodPost, "/api/v1/coupons/preview", map[string]any{"code": ownCode, "talent_id": talentID}, http.StatusOK)
	if preview["discount_amount"] != float64(5_000) {
		t.Fatalf("원래 코드 미리보기=%v", preview)
	}
}

func TestIntegrationReportTakesDownATalentAndHidesTheSeller(t *testing.T) {
	server, pool := integrationServer(t)
	keyword := uniqueName("신고검증")
	seller, talentID, _ := sellTalent(t, server, "reportseller", keyword+" 문제 상품", 60_000)
	reporter := newClient(t, server.URL)
	reporter.register(uniqueName("reporter"))

	reporter.do(http.MethodPost, "/api/v1/reports", map[string]any{
		"resource_type": "talent", "resource_id": talentID, "reason": "fraud", "details": "설명과 다른 결과물을 판매합니다.", "evidence": []any{},
	}, http.StatusCreated)
	// The same reporter cannot pile duplicates onto one open case.
	reporter.do(http.MethodPost, "/api/v1/reports", map[string]any{
		"resource_type": "talent", "resource_id": talentID, "reason": "spam", "details": "다시 신고", "evidence": []any{},
	}, http.StatusConflict)

	operator := operatorClient(t, server, pool, "reportop")
	queue := operator.do(http.MethodGet, "/api/v1/admin/reports?state=open", nil, http.StatusOK)
	var reportID string
	for _, raw := range queue["items"].([]any) {
		item, _ := raw.(map[string]any)
		if item["resource_id"] == talentID {
			reportID, _ = item["id"].(string)
			if item["resource_label"] == "" || item["reporter_name"] == "" {
				t.Fatalf("신고 대기열 항목에 맥락이 없습니다: %v", item)
			}
		}
	}
	if reportID == "" {
		t.Fatal("신고가 운영 대기열에 없습니다")
	}
	operator.do(http.MethodPost, "/api/v1/admin/reports/"+reportID+"/resolve", map[string]any{"action": "hide_talent", "note": "검토 결과 비공개"}, http.StatusOK)

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM talents WHERE id=$1`, talentID).Scan(&status); err != nil {
		t.Fatalf("talent query: %v", err)
	}
	if status != "paused" {
		t.Fatalf("상품 상태=%s", status)
	}
	if found := reporter.do(http.MethodGet, "/api/v1/talents?q="+keyword, nil, http.StatusOK); len(found["items"].([]any)) != 0 {
		t.Fatal("비공개 상품이 검색에 남아 있습니다")
	}
	// The reporter learns the outcome through the same notification pipeline.
	eventually(t, "신고 처리 알림", 20*time.Second, func() bool {
		inbox := reporter.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
		for _, raw := range inbox["items"].([]any) {
			if item, _ := raw.(map[string]any); item["event_type"] == "ReportResolved" {
				return true
			}
		}
		return false
	})
	mine := reporter.do(http.MethodGet, "/api/v1/me/reports", nil, http.StatusOK)
	first, _ := mine["items"].([]any)[0].(map[string]any)
	if first["state"] != "resolved" || first["resolution"] == nil {
		t.Fatalf("내 신고 상태=%v", first)
	}

	// Restoring the product puts it back in front of buyers.
	operator.do(http.MethodPost, "/api/v1/admin/talents/"+talentID+"/status", map[string]any{"status": "published", "note": ""}, http.StatusOK)
	if found := reporter.do(http.MethodGet, "/api/v1/talents?q="+keyword, nil, http.StatusOK); len(found["items"].([]any)) != 1 {
		t.Fatal("복구한 상품이 검색에 없습니다")
	}

	// Suspending the seller hides their catalogue without touching each product.
	second := newClient(t, server.URL)
	second.register(uniqueName("reporter2"))
	second.do(http.MethodPost, "/api/v1/reports", map[string]any{
		"resource_type": "talent", "resource_id": talentID, "reason": "fraud", "details": "반복 위반", "evidence": []any{},
	}, http.StatusCreated)
	queue = operator.do(http.MethodGet, "/api/v1/admin/reports?state=open", nil, http.StatusOK)
	for _, raw := range queue["items"].([]any) {
		if item, _ := raw.(map[string]any); item["resource_id"] == talentID {
			reportID, _ = item["id"].(string)
		}
	}
	operator.do(http.MethodPost, "/api/v1/admin/reports/"+reportID+"/resolve", map[string]any{"action": "suspend_user", "note": "반복 위반"}, http.StatusOK)
	var sellerStatus string
	if err := pool.QueryRow(context.Background(), `SELECT u.status FROM users u JOIN talents t ON t.seller_id=u.id WHERE t.id=$1`, talentID).Scan(&sellerStatus); err != nil {
		t.Fatalf("seller query: %v", err)
	}
	if sellerStatus != "suspended" {
		t.Fatalf("판매자 상태=%s", sellerStatus)
	}
	if found := second.do(http.MethodGet, "/api/v1/talents?q="+keyword, nil, http.StatusOK); len(found["items"].([]any)) != 0 {
		t.Fatal("정지된 판매자의 상품이 검색에 남아 있습니다")
	}
	// A suspended seller's session is revoked, so their own calls stop working.
	seller.do(http.MethodGet, "/api/v1/me", nil, http.StatusUnauthorized)
}

// mcpClient calls the MCP endpoint with an API key, the way an agent would.
type mcpClient struct {
	t    *testing.T
	base string
	key  string
	http *http.Client
	next int
}

func (m *mcpClient) call(method string, params any) map[string]any {
	m.t.Helper()
	m.next++
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.next, "method": method, "params": params})
	if err != nil {
		m.t.Fatalf("encode %s: %v", method, err)
	}
	request, _ := http.NewRequest(http.MethodPost, m.base+"/mcp", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+m.key)
	response, err := m.http.Do(request)
	if err != nil {
		m.t.Fatalf("%s: %v", method, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		m.t.Fatalf("%s status=%d body=%s", method, response.StatusCode, payload)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		m.t.Fatalf("decode %s: %v body=%s", method, err, payload)
	}
	if rpcError, ok := decoded["error"].(map[string]any); ok {
		m.t.Fatalf("%s rpc error: %v", method, rpcError)
	}
	return decoded
}

// tool returns the structured content of a successful tool call.
func (m *mcpClient) tool(name string, arguments map[string]any) map[string]any {
	m.t.Helper()
	response := m.call("tools/call", map[string]any{"name": name, "arguments": arguments})
	result, _ := response["result"].(map[string]any)
	if isError, _ := result["isError"].(bool); isError {
		m.t.Fatalf("%s failed: %v", name, result["content"])
	}
	structured, _ := result["structuredContent"].(map[string]any)
	return structured
}

func (m *mcpClient) toolError(name string, arguments map[string]any) string {
	m.t.Helper()
	response := m.call("tools/call", map[string]any{"name": name, "arguments": arguments})
	result, _ := response["result"].(map[string]any)
	if isError, _ := result["isError"].(bool); !isError {
		m.t.Fatalf("%s는 실패해야 합니다: %v", name, result)
	}
	content, _ := result["content"].([]any)
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

func TestIntegrationAgentCompletesAPurchaseOverMCP(t *testing.T) {
	server, pool := integrationServer(t)
	operator := operatorClient(t, server, pool, "mcpop")
	code := strings.ToUpper(uniqueName("AGENT"))
	operator.do(http.MethodPost, "/api/v1/admin/coupons", map[string]any{
		"code": code, "name": "에이전트 테스트 할인", "discount_type": "fixed", "discount_value": 10_000,
		"min_order_amount": 0, "per_user_limit": 2, "active": true,
	}, http.StatusCreated)

	keyword := uniqueName("에이전트검증")
	seller, talentID, _ := sellTalent(t, server, "mcpseller", keyword+" 서비스", 100_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("mcpbuyer"))
	created := buyer.do(http.MethodPost, "/api/v1/me/api-keys", map[string]any{
		"name": "에이전트 키", "scopes": []string{"mcp.use", "orders.buy"}, "allowed_cidrs": []string{}, "rate_limit_per_minute": 600,
	}, http.StatusCreated)
	secret, _ := created["secret"].(string)
	if secret == "" {
		t.Fatal("API 키 원문이 반환되지 않았습니다")
	}
	agent := &mcpClient{t: t, base: server.URL, key: secret, http: &http.Client{Timeout: 20 * time.Second}}

	catalogue, _ := agent.call("tools/list", map[string]any{})["result"].(map[string]any)
	tools, _ := catalogue["tools"].([]any)
	names := map[string]bool{}
	for _, raw := range tools {
		if tool, _ := raw.(map[string]any); tool != nil {
			names[fmt.Sprint(tool["name"])] = true
		}
	}
	for _, expected := range []string{"list_orders", "pay_order", "preview_coupon", "list_notifications", "open_dispute", "submit_report"} {
		if !names[expected] {
			t.Fatalf("%s 도구가 목록에 없습니다", expected)
		}
	}

	found := agent.tool("search_talents", map[string]any{"query": keyword})
	if items, _ := found["items"].([]any); len(items) != 1 {
		t.Fatalf("검색 결과 %d건", len(items))
	}

	preview := agent.tool("preview_coupon", map[string]any{"code": code, "talent_id": talentID})
	if preview["payable_amount"] != float64(90_000) {
		t.Fatalf("쿠폰 미리보기=%v", preview)
	}

	order := agent.tool("create_order", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "에이전트 주문"}, "coupon_code": code,
	})
	orderID := fmt.Sprint(order["id"])
	if order["payable_amount"] != float64(90_000) {
		t.Fatalf("주문 결제 예정액=%v", order["payable_amount"])
	}

	// The same idempotency key must not charge twice, which is the whole reason
	// the tool requires the agent to supply one.
	key := uniqueName("agentpay")
	paid := agent.tool("pay_order", map[string]any{"order_id": orderID, "idempotency_key": key})
	if paid["amount"] != float64(90_000) {
		t.Fatalf("결제 금액=%v", paid["amount"])
	}
	replay := agent.tool("pay_order", map[string]any{"order_id": orderID, "idempotency_key": key})
	if replay["idempotent_replay"] != true {
		t.Fatalf("같은 키 재시도가 재생으로 처리되지 않았습니다: %v", replay)
	}
	var payments int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payments WHERE order_id=$1`, orderID).Scan(&payments); err != nil {
		t.Fatalf("payment query: %v", err)
	}
	if payments != 1 {
		t.Fatalf("결제 레코드 %d건", payments)
	}
	if _, total := ledgerBalance(t, pool, orderID); total != 0 {
		t.Fatalf("에이전트 결제 후 원장 합계=%d", total)
	}

	mine := agent.tool("list_orders", map[string]any{})
	if items, _ := mine["items"].([]any); len(items) != 1 {
		t.Fatalf("주문 목록 %d건", len(items))
	}

	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "IN_PROGRESS", "note": ""}, http.StatusOK)
	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/deliveries", map[string]any{"delivery_type": "text", "content": map[string]any{"text": "결과물"}, "description": "납품"}, http.StatusCreated)

	// The agent notices the delivery through its own inbox rather than polling
	// the order, which is how a long running agent would watch for changes.
	eventually(t, "에이전트 알림 수신", 20*time.Second, func() bool {
		inbox := agent.tool("list_notifications", map[string]any{"unread_only": true})
		for _, raw := range inbox["items"].([]any) {
			if item, _ := raw.(map[string]any); item["event_type"] == "OrderDELIVERED" {
				return true
			}
		}
		return false
	})

	agent.tool("open_dispute", map[string]any{"order_id": orderID, "reason": "결과물이 요구사항과 다릅니다."})
	var disputes int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM disputes WHERE order_id=$1 AND state='open'`, orderID).Scan(&disputes); err != nil {
		t.Fatalf("dispute query: %v", err)
	}
	if disputes != 1 {
		t.Fatalf("분쟁 %d건", disputes)
	}

	// A key without the selling scope must not be able to deliver work.
	message := agent.toolError("submit_delivery", map[string]any{"order_id": orderID, "delivery_type": "text", "content": map[string]any{"text": "x"}})
	if !strings.Contains(message, "orders.sell") {
		t.Fatalf("권한 오류 메시지=%q", message)
	}
	if missing := agent.toolError("pay_order", map[string]any{"order_id": orderID, "idempotency_key": ""}); !strings.Contains(missing, "idempotency_key") {
		t.Fatalf("idempotency 안내 메시지=%q", missing)
	}
}

func TestIntegrationAuditLogPagesWithoutGapsOrRepeats(t *testing.T) {
	server, pool := integrationServer(t)
	name := uniqueName("auditor")
	auditor := newClient(t, server.URL)
	auditor.register(name)
	grantRole(t, pool, name, "security_admin")
	auditor.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	auditor.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23"}, http.StatusOK)

	// Registrations and logins each write an audit row, so a handful of extra
	// accounts gives the pager something to walk through.
	for i := 0; i < 6; i++ {
		extra := newClient(t, server.URL)
		extra.register(uniqueName("noise"))
	}

	// The audit table is shared with every other test in this package, so the
	// walk has to be able to reach the end of whatever is already there.
	var expected int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs`).Scan(&expected); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	const pageSize = 25
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for pages < expected/pageSize+5 {
		path := "/api/v1/admin/audit?limit=25"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		page := auditor.do(http.MethodGet, path, nil, http.StatusOK)
		items, _ := page["items"].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			id := fmt.Sprint(item["id"])
			if seen[id] {
				t.Fatalf("같은 감사 기록이 두 페이지에 나왔습니다: %s", id)
			}
			seen[id] = true
		}
		pages++
		next, _ := page["next_cursor"].(string)
		if next == "" {
			if len(items) > pageSize {
				t.Fatalf("마지막 페이지 크기=%d", len(items))
			}
			break
		}
		if len(items) != pageSize {
			t.Fatalf("커서가 있는데 페이지 크기=%d", len(items))
		}
		cursor = next
	}
	if pages < 2 {
		t.Fatalf("페이지가 %d개뿐입니다. 커서가 동작하지 않았을 수 있습니다", pages)
	}
	if len(seen) != expected {
		t.Fatalf("페이지로 읽은 기록 %d건, 실제 %d건", len(seen), expected)
	}

	// A bookmark that no longer decodes must return the newest page instead of
	// an error, so a stale UI link cannot break the console.
	fallback := auditor.do(http.MethodGet, "/api/v1/admin/audit?limit=3&cursor=broken", nil, http.StatusOK)
	if items, _ := fallback["items"].([]any); len(items) != 3 {
		t.Fatalf("잘못된 커서 응답 크기=%d", len(items))
	}
}

func TestIntegrationCouponPreviewIsThrottled(t *testing.T) {
	server, pool := integrationServer(t)
	operator := operatorClient(t, server, pool, "throttleop")
	code := strings.ToUpper(uniqueName("THROTTLE"))
	operator.do(http.MethodPost, "/api/v1/admin/coupons", map[string]any{
		"code": code, "name": "한도 검증", "discount_type": "fixed", "discount_value": 1_000,
		"min_order_amount": 0, "per_user_limit": 5, "active": true,
	}, http.StatusCreated)
	_, talentID, _ := sellTalent(t, server, "throttleseller", "한도 검증 상품", 50_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("throttlebuyer"))

	// Guessing codes is the abuse this limit exists for, so the wrong guesses
	// count against the caller just like the right ones.
	limit := throttleDefaults["coupon_preview"]
	for i := 0; i < limit; i++ {
		want := http.StatusOK
		guess := code
		if i%2 == 1 {
			guess = strings.ToUpper(uniqueName("WRONG"))
			want = http.StatusConflict
		}
		buyer.do(http.MethodPost, "/api/v1/coupons/preview", map[string]any{"code": guess, "talent_id": talentID}, want)
	}
	buyer.do(http.MethodPost, "/api/v1/coupons/preview", map[string]any{"code": code, "talent_id": talentID}, http.StatusTooManyRequests)

	// The limit is per account, so a different buyer is unaffected.
	other := newClient(t, server.URL)
	other.register(uniqueName("throttleother"))
	other.do(http.MethodPost, "/api/v1/coupons/preview", map[string]any{"code": code, "talent_id": talentID}, http.StatusOK)
}

func TestIntegrationOrganizationBudgetCapsAndReleasesSpend(t *testing.T) {
	server, pool := integrationServer(t)
	owner := newClient(t, server.URL)
	owner.register(uniqueName("orgowner"))
	memberName := uniqueName("orgmember")
	member := newClient(t, server.URL)
	member.register(memberName)

	organization := owner.do(http.MethodPost, "/api/v1/organizations", map[string]any{"name": uniqueName("한빛 주식회사")}, http.StatusCreated)
	orgID := fmt.Sprint(organization["id"])
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/members", map[string]any{"account": memberName, "role": "member"}, http.StatusCreated)
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/budgets", map[string]any{"name": "2026 상반기", "amount": 150_000}, http.StatusCreated)

	_, talentID, _ := sellTalent(t, server, "orgseller", "조직 검증 상품", 100_000)

	first := member.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}, "organization_id": orgID,
	}, http.StatusCreated)
	firstID := fmt.Sprint(first["id"])
	if first["budget_reserved"] != float64(100_000) {
		t.Fatalf("예약 금액=%v", first["budget_reserved"])
	}

	// 50,000 is left, so a second 100,000 order must be refused rather than
	// quietly overspending the company's budget.
	member.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}, "organization_id": orgID,
	}, http.StatusConflict)

	detail := owner.do(http.MethodGet, "/api/v1/organizations/"+orgID, nil, http.StatusOK)
	budgets, _ := detail["budgets"].([]any)
	budget, _ := budgets[0].(map[string]any)
	if budget["consumed_amount"] != float64(100_000) || budget["remaining_amount"] != float64(50_000) {
		t.Fatalf("예산 상태=%v", budget)
	}

	// Cancelling frees the reservation so the same budget can be spent again.
	operator := operatorClient(t, server, pool, "orgop")
	operator.do(http.MethodPost, "/api/v1/orders/"+firstID+"/transition", map[string]any{"to": "CANCELLED", "note": "테스트 취소"}, http.StatusOK)
	var consumed int64
	if err := pool.QueryRow(context.Background(), `SELECT consumed_amount FROM budgets WHERE organization_id=$1`, orgID).Scan(&consumed); err != nil {
		t.Fatalf("budget query: %v", err)
	}
	if consumed != 0 {
		t.Fatalf("취소 후 사용액=%d", consumed)
	}
	// Releasing twice must not hand back more than the order ever held.
	operator.do(http.MethodPost, "/api/v1/orders/"+firstID+"/transition", map[string]any{"to": "CANCELLED", "note": "중복"}, http.StatusConflict)

	second := member.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}, "organization_id": orgID,
	}, http.StatusCreated)
	secondID := fmt.Sprint(second["id"])

	// A refund returns only what the organization actually paid.
	member.doWithHeaders(http.MethodPost, "/api/v1/orders/"+secondID+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("orgpay")})
	seller := newClient(t, server.URL)
	_ = seller
	operator.do(http.MethodPost, "/api/v1/orders/"+secondID+"/transition", map[string]any{"to": "IN_PROGRESS", "note": ""}, http.StatusOK)
	operator.do(http.MethodPost, "/api/v1/orders/"+secondID+"/transition", map[string]any{"to": "DISPUTED", "note": "검증"}, http.StatusOK)
	dispute := member.do(http.MethodGet, "/api/v1/orders/"+secondID+"/disputes", nil, http.StatusOK)
	if items, _ := dispute["items"].([]any); len(items) != 0 {
		t.Fatalf("상태 전이만으로 분쟁 기록이 생겼습니다: %v", items)
	}
	if err := pool.QueryRow(context.Background(), `SELECT consumed_amount FROM budgets WHERE organization_id=$1`, orgID).Scan(&consumed); err != nil {
		t.Fatalf("budget query: %v", err)
	}
	if consumed != 100_000 {
		t.Fatalf("분쟁 중 사용액=%d", consumed)
	}
	operator.do(http.MethodPost, "/api/v1/orders/"+secondID+"/transition", map[string]any{"to": "REFUNDED", "note": "환불"}, http.StatusOK)
	if err := pool.QueryRow(context.Background(), `SELECT consumed_amount FROM budgets WHERE organization_id=$1`, orgID).Scan(&consumed); err != nil {
		t.Fatalf("budget query: %v", err)
	}
	if consumed != 0 {
		t.Fatalf("환불 후 사용액=%d", consumed)
	}
}

func TestIntegrationOrganizationAccessAndOwnerProtection(t *testing.T) {
	server, _ := integrationServer(t)
	owner := newClient(t, server.URL)
	ownerName := uniqueName("protowner")
	owner.register(ownerName)
	organization := owner.do(http.MethodPost, "/api/v1/organizations", map[string]any{"name": uniqueName("보호 검증사")}, http.StatusCreated)
	orgID := fmt.Sprint(organization["id"])

	// An organization that lost its last owner could never be administered
	// again, so both demotion and removal are refused.
	ownerID := owner.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)["id"]
	owner.do(http.MethodPatch, "/api/v1/organizations/"+orgID+"/members/"+fmt.Sprint(ownerID), map[string]any{"role": "member"}, http.StatusConflict)
	owner.do(http.MethodDelete, "/api/v1/organizations/"+orgID+"/members/"+fmt.Sprint(ownerID), nil, http.StatusConflict)

	// A non member must not even learn the organization exists.
	outsider := newClient(t, server.URL)
	outsider.register(uniqueName("outsider"))
	outsider.do(http.MethodGet, "/api/v1/organizations/"+orgID, nil, http.StatusNotFound)
	outsider.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/budgets", map[string]any{"name": "x", "amount": 1000}, http.StatusNotFound)

	memberName := uniqueName("plainmember")
	plain := newClient(t, server.URL)
	plain.register(memberName)
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/members", map[string]any{"account": memberName, "role": "member"}, http.StatusCreated)
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/members", map[string]any{"account": memberName, "role": "member"}, http.StatusConflict)
	// Members can read the organization but not spend or staff it.
	plain.do(http.MethodGet, "/api/v1/organizations/"+orgID, nil, http.StatusOK)
	plain.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/budgets", map[string]any{"name": "몰래", "amount": 1000}, http.StatusForbidden)
	plain.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/members", map[string]any{"account": ownerName, "role": "member"}, http.StatusForbidden)

	mine := plain.do(http.MethodGet, "/api/v1/me/organizations", nil, http.StatusOK)
	items, _ := mine["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("내 조직 %d건", len(items))
	}
	if entry, _ := items[0].(map[string]any); entry["role"] != "member" || entry["member_count"] != float64(2) {
		t.Fatalf("조직 요약=%v", items[0])
	}
}

func TestIntegrationMaintenanceExpiresAbandonedOrdersAndAutoAccepts(t *testing.T) {
	server, pool := integrationServer(t)
	background := context.Background()
	if _, err := pool.Exec(background, `UPDATE system_settings SET value = value || '{"order_expiry_hours":1,"auto_accept_days":1}'::jsonb WHERE key='marketplace.policy'`); err != nil {
		t.Fatalf("policy update: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(background, `UPDATE system_settings SET value = value || '{"order_expiry_hours":72,"auto_accept_days":0}'::jsonb WHERE key='marketplace.policy'`)
	})

	operator := operatorClient(t, server, pool, "maintop")
	code := strings.ToUpper(uniqueName("EXPIRE"))
	operator.do(http.MethodPost, "/api/v1/admin/coupons", map[string]any{
		"code": code, "name": "만료 검증", "discount_type": "fixed", "discount_value": 10_000,
		"min_order_amount": 0, "per_user_limit": 1, "active": true,
	}, http.StatusCreated)

	owner := newClient(t, server.URL)
	owner.register(uniqueName("maintowner"))
	organization := owner.do(http.MethodPost, "/api/v1/organizations", map[string]any{"name": uniqueName("만료 검증사")}, http.StatusCreated)
	orgID := fmt.Sprint(organization["id"])
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/budgets", map[string]any{"name": "검증 예산", "amount": 200_000}, http.StatusCreated)

	seller, talentID, _ := sellTalent(t, server, "maintseller", "만료 검증 상품", 100_000)
	abandoned := owner.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{},
		"organization_id": orgID, "coupon_code": code,
	}, http.StatusCreated)
	abandonedID := fmt.Sprint(abandoned["id"])

	// The one use of the coupon is taken and the budget is reserved.
	owner.do(http.MethodPost, "/api/v1/coupons/preview", map[string]any{"code": code, "talent_id": talentID}, http.StatusConflict)
	var consumed int64
	if err := pool.QueryRow(background, `SELECT consumed_amount FROM budgets WHERE organization_id=$1`, orgID).Scan(&consumed); err != nil {
		t.Fatalf("budget query: %v", err)
	}
	if consumed != 90_000 {
		t.Fatalf("예약 금액=%d", consumed)
	}

	// Age the order past the expiry window instead of waiting for it.
	if _, err := pool.Exec(background, `UPDATE orders SET created_at=now()-interval '2 hours' WHERE id=$1`, abandonedID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	operator.do(http.MethodPost, "/api/v1/admin/risk/rescan", map[string]any{}, http.StatusOK)

	var state string
	if err := pool.QueryRow(background, `SELECT state FROM orders WHERE id=$1`, abandonedID).Scan(&state); err != nil {
		t.Fatalf("order query: %v", err)
	}
	if state != "CANCELLED" {
		t.Fatalf("방치된 주문 상태=%s", state)
	}
	if err := pool.QueryRow(background, `SELECT consumed_amount FROM budgets WHERE organization_id=$1`, orgID).Scan(&consumed); err != nil {
		t.Fatalf("budget query: %v", err)
	}
	if consumed != 0 {
		t.Fatalf("만료 후 예산 사용액=%d", consumed)
	}
	// The buyer gets their coupon use back rather than losing it to an order
	// they never paid for.
	owner.do(http.MethodPost, "/api/v1/coupons/preview", map[string]any{"code": code, "talent_id": talentID}, http.StatusOK)
	var systemAudits int
	if err := pool.QueryRow(background, `SELECT count(*) FROM audit_logs WHERE action='order.expire' AND resource_id=$1 AND actor_user_id IS NULL`, abandonedID).Scan(&systemAudits); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if systemAudits != 1 {
		t.Fatalf("시스템 감사 기록 %d건", systemAudits)
	}

	// A delivered order the buyer never answers settles to the seller.
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("maintbuyer"))
	silentID := payAndDeliver(t, buyer, seller, talentID)
	disputedID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+disputedID+"/disputes", map[string]any{"reason": "결과물이 요구사항과 다릅니다.", "evidence": []any{}}, http.StatusCreated)
	if _, err := pool.Exec(background, `UPDATE orders SET updated_at=now()-interval '3 days' WHERE id=ANY($1)`, []string{silentID, disputedID}); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	operator.do(http.MethodPost, "/api/v1/admin/risk/rescan", map[string]any{}, http.StatusOK)

	if err := pool.QueryRow(background, `SELECT state FROM orders WHERE id=$1`, silentID).Scan(&state); err != nil {
		t.Fatalf("order query: %v", err)
	}
	if state != "ACCEPTED" {
		t.Fatalf("자동 구매확정 후 상태=%s", state)
	}
	var settlements int
	if err := pool.QueryRow(background, `SELECT count(*) FROM settlements WHERE order_id=$1`, silentID).Scan(&settlements); err != nil {
		t.Fatalf("settlement query: %v", err)
	}
	if settlements != 1 {
		t.Fatalf("자동 확정 정산 %d건", settlements)
	}
	if _, total := ledgerBalance(t, pool, silentID); total != 0 {
		t.Fatalf("자동 확정 후 원장 합계=%d", total)
	}
	// An order under dispute is the buyer objecting, so it must never auto accept.
	if err := pool.QueryRow(background, `SELECT state FROM orders WHERE id=$1`, disputedID).Scan(&state); err != nil {
		t.Fatalf("order query: %v", err)
	}
	if state != "DISPUTED" {
		t.Fatalf("분쟁 중 주문 상태=%s", state)
	}
}

func TestIntegrationSellerSeesEarningsAndHolds(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "earnseller", "정산 조회 상품", 200_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("earnbuyer"))

	// A buyer has no selling scope, so the seller view stays closed to them.
	buyer.do(http.MethodGet, "/api/v1/me/settlements", nil, http.StatusForbidden)

	empty := seller.do(http.MethodGet, "/api/v1/me/settlements", nil, http.StatusOK)
	if items, _ := empty["items"].([]any); len(items) != 0 {
		t.Fatalf("거래 전 정산 %d건", len(items))
	}

	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)

	earnings := seller.do(http.MethodGet, "/api/v1/me/settlements", nil, http.StatusOK)
	items, _ := earnings["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("정산 %d건", len(items))
	}
	entry, _ := items[0].(map[string]any)
	// The seller must see the split, not just a single number.
	if entry["gross_amount"] != float64(200_000) || entry["platform_fee"] != float64(20_000) || entry["net_amount"] != float64(180_000) {
		t.Fatalf("정산 금액 구성=%v", entry)
	}
	if entry["state"] != "scheduled" || entry["state_label"] != "지급 예정" {
		t.Fatalf("정산 상태=%v", entry["state"])
	}
	if entry["scheduled_at"] == nil {
		t.Fatal("지급 예정일이 없습니다")
	}
	summary, _ := earnings["summary"].(map[string]any)
	if summary["upcoming_amount"] != float64(180_000) || summary["held_amount"] != float64(0) || summary["paid_amount"] != float64(0) {
		t.Fatalf("수익 요약=%v", summary)
	}
	if summary["lifetime_fee"] != float64(20_000) {
		t.Fatalf("누적 수수료=%v", summary["lifetime_fee"])
	}

	// A held payout must tell the seller why, not just stop arriving.
	operator := operatorClient(t, server, pool, "earnop")
	settlementID := fmt.Sprint(entry["id"])
	operator.do(http.MethodPost, "/api/v1/admin/settlements/"+settlementID+"/action", map[string]any{"action": "hold", "reason": "본인 확인 서류 필요"}, http.StatusOK)
	held := seller.do(http.MethodGet, "/api/v1/me/settlements?state=hold", nil, http.StatusOK)
	heldItems, _ := held["items"].([]any)
	if len(heldItems) != 1 {
		t.Fatalf("보류 정산 %d건", len(heldItems))
	}
	heldEntry, _ := heldItems[0].(map[string]any)
	if heldEntry["hold_reason"] != "본인 확인 서류 필요" {
		t.Fatalf("보류 사유=%v", heldEntry["hold_reason"])
	}
	heldSummary, _ := held["summary"].(map[string]any)
	if heldSummary["held_amount"] != float64(180_000) || heldSummary["upcoming_amount"] != float64(0) {
		t.Fatalf("보류 후 요약=%v", heldSummary)
	}

	operator.do(http.MethodPost, "/api/v1/admin/settlements/"+settlementID+"/action", map[string]any{"action": "release"}, http.StatusOK)
	operator.do(http.MethodPost, "/api/v1/admin/settlements/"+settlementID+"/action", map[string]any{"action": "complete"}, http.StatusOK)
	paid := seller.do(http.MethodGet, "/api/v1/me/settlements", nil, http.StatusOK)
	paidSummary, _ := paid["summary"].(map[string]any)
	if paidSummary["paid_amount"] != float64(180_000) || paidSummary["upcoming_amount"] != float64(0) {
		t.Fatalf("지급 후 요약=%v", paidSummary)
	}

	// One seller's money is not another seller's business.
	other, _, _ := sellTalent(t, server, "othersell", "다른 판매자 상품", 10_000)
	if isolated := other.do(http.MethodGet, "/api/v1/me/settlements", nil, http.StatusOK); len(isolated["items"].([]any)) != 0 {
		t.Fatal("다른 판매자의 정산이 보입니다")
	}

	// The same view reaches an agent seller through MCP.
	key := seller.do(http.MethodPost, "/api/v1/me/api-keys", map[string]any{
		"name": "판매자 에이전트", "scopes": []string{"mcp.use", "orders.sell"}, "allowed_cidrs": []string{}, "rate_limit_per_minute": 600,
	}, http.StatusCreated)
	agent := &mcpClient{t: t, base: server.URL, key: fmt.Sprint(key["secret"]), http: &http.Client{Timeout: 20 * time.Second}}
	viaAgent := agent.tool("list_settlements", map[string]any{})
	if agentItems, _ := viaAgent["items"].([]any); len(agentItems) != 1 {
		t.Fatalf("에이전트가 본 정산 %d건", len(agentItems))
	}
}

func TestIntegrationCancellingAPaidOrderReturnsEscrow(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "cancelseller", "취소 검증 상품", 120_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("cancelbuyer"))
	order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{},
	}, http.StatusCreated)
	orderID := fmt.Sprint(order["id"])
	buyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+orderID+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("cancelpay")})

	// Escrow is holding the buyer's money at this point.
	balances, _ := ledgerBalance(t, pool, orderID)
	if balances["Escrow"] != -120_000 {
		t.Fatalf("결제 후 에스크로 잔액=%d", balances["Escrow"])
	}

	// Cancelling a paid order must give the money back, not strand it in escrow.
	operator := operatorClient(t, server, pool, "cancelop")
	operator.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "CANCEL_REQUESTED", "note": "구매자 요청"}, http.StatusOK)
	operator.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "CANCELLED", "note": "합의 취소"}, http.StatusOK)

	balances, total := ledgerBalance(t, pool, orderID)
	if total != 0 {
		t.Fatalf("취소 후 원장 합계=%d (%v)", total, balances)
	}
	if balances["Escrow"] != 0 {
		t.Fatalf("취소 후에도 에스크로에 %d원이 남았습니다", -balances["Escrow"])
	}
	if balances["Buyer Refund"] != -120_000 {
		t.Fatalf("구매자 환불 잔액=%d", balances["Buyer Refund"])
	}
	var refunded int64
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(sum(amount),0) FROM refunds WHERE order_id=$1`, orderID).Scan(&refunded); err != nil {
		t.Fatalf("refund query: %v", err)
	}
	if refunded != 120_000 {
		t.Fatalf("환불 기록 금액=%d", refunded)
	}

	// With a coupon the buyer only gets back what they actually paid, and the
	// organization's budget follows that same share.
	code := strings.ToUpper(uniqueName("CANCELCUT"))
	operator.do(http.MethodPost, "/api/v1/admin/coupons", map[string]any{
		"code": code, "name": "취소 검증 할인", "discount_type": "fixed", "discount_value": 20_000,
		"min_order_amount": 0, "per_user_limit": 1, "active": true,
	}, http.StatusCreated)
	organization := buyer.do(http.MethodPost, "/api/v1/organizations", map[string]any{"name": uniqueName("취소 검증사")}, http.StatusCreated)
	orgID := fmt.Sprint(organization["id"])
	buyer.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/budgets", map[string]any{"name": "취소 예산", "amount": 300_000}, http.StatusCreated)
	discounted := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{},
		"organization_id": orgID, "coupon_code": code,
	}, http.StatusCreated)
	discountedID := fmt.Sprint(discounted["id"])
	buyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+discountedID+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("cancelpay2")})
	operator.do(http.MethodPost, "/api/v1/orders/"+discountedID+"/transition", map[string]any{"to": "CANCEL_REQUESTED", "note": "요청"}, http.StatusOK)
	operator.do(http.MethodPost, "/api/v1/orders/"+discountedID+"/transition", map[string]any{"to": "CANCELLED", "note": "합의"}, http.StatusOK)

	balances, total = ledgerBalance(t, pool, discountedID)
	if total != 0 || balances["Escrow"] != 0 {
		t.Fatalf("할인 주문 취소 후 원장=%v (합계 %d)", balances, total)
	}
	if balances["Buyer Refund"] != -100_000 || balances["Promotion Recovery"] != -20_000 {
		t.Fatalf("할인 주문 환불 배분=%v", balances)
	}
	var consumed int64
	if err := pool.QueryRow(context.Background(), `SELECT consumed_amount FROM budgets WHERE organization_id=$1`, orgID).Scan(&consumed); err != nil {
		t.Fatalf("budget query: %v", err)
	}
	if consumed != 0 {
		t.Fatalf("취소 후 예산 사용액=%d", consumed)
	}
	_ = seller
}

func TestIntegrationEveryPathToAcceptedBooksTheSettlement(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "pathseller", "확정 경로 상품", 90_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("pathbuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)

	// The generic transition endpoint used to move an order to ACCEPTED without
	// creating a settlement, leaving the seller unpaid and escrow stranded.
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "ACCEPTED", "note": "직접 전이"}, http.StatusOK)

	var state string
	if err := pool.QueryRow(context.Background(), `SELECT state FROM orders WHERE id=$1`, orderID).Scan(&state); err != nil {
		t.Fatalf("order query: %v", err)
	}
	if state != "ACCEPTED" {
		t.Fatalf("주문 상태=%s", state)
	}
	var net int64
	if err := pool.QueryRow(context.Background(), `SELECT net_amount FROM settlements WHERE order_id=$1`, orderID).Scan(&net); err != nil {
		t.Fatalf("전이 경로에서 정산이 만들어지지 않았습니다: %v", err)
	}
	if net != 81_000 {
		t.Fatalf("정산 실수령=%d", net)
	}
	balances, total := ledgerBalance(t, pool, orderID)
	if total != 0 || balances["Escrow"] != 0 {
		t.Fatalf("확정 후 원장=%v (합계 %d)", balances, total)
	}

	// A disputed order cannot be quietly completed; its money decision has to go
	// through dispute resolution.
	disputedID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+disputedID+"/disputes", map[string]any{"reason": "결과물이 요구사항과 다릅니다.", "evidence": []any{}}, http.StatusCreated)
	operator := operatorClient(t, server, pool, "pathop")
	operator.do(http.MethodPost, "/api/v1/orders/"+disputedID+"/transition", map[string]any{"to": "COMPLETED", "note": "그냥 종료"}, http.StatusConflict)
	if err := pool.QueryRow(context.Background(), `SELECT state FROM orders WHERE id=$1`, disputedID).Scan(&state); err != nil {
		t.Fatalf("order query: %v", err)
	}
	if state != "DISPUTED" {
		t.Fatalf("분쟁 주문 상태=%s", state)
	}
}

func TestIntegrationAcceptingAQuoteCreatesAnOrder(t *testing.T) {
	server, pool := integrationServer(t)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("quotebuyer"))
	winner, winnerTalent, _ := sellTalent(t, server, "winseller", "견적 낙찰 상품", 500_000)
	loser, loserTalent, _ := sellTalent(t, server, "loseseller", "견적 탈락 상품", 600_000)

	rfq := buyer.do(http.MethodPost, "/api/v1/rfqs", map[string]any{
		"title": uniqueName("사내 시스템 구축"), "description": "관리자 화면과 보고서를 포함한 시스템이 필요합니다.",
		"requirements": map[string]any{}, "budget_max": 700_000, "currency": "KRW",
	}, http.StatusCreated)
	rfqID := fmt.Sprint(rfq["id"])

	// A quote with no service attached cannot become an order, so the seller is
	// told rather than the buyer hitting a dead end later.
	winner.do(http.MethodPost, "/api/v1/quotes", map[string]any{
		"rfq_id": rfqID, "amount": 450_000, "currency": "KRW", "delivery_days": 14, "scope": map[string]any{"description": "1차"}, "milestones": []any{},
	}, http.StatusCreated)
	quotes := buyer.do(http.MethodGet, "/api/v1/rfqs/"+rfqID+"/quotes", nil, http.StatusOK)
	first, _ := quotes["items"].([]any)[0].(map[string]any)
	buyer.do(http.MethodPost, "/api/v1/quotes/"+fmt.Sprint(first["id"])+"/accept", map[string]any{}, http.StatusConflict)

	// Re-submitting attaches the service the work will be delivered through.
	winner.do(http.MethodPost, "/api/v1/quotes", map[string]any{
		"rfq_id": rfqID, "amount": 450_000, "currency": "KRW", "delivery_days": 14,
		"scope": map[string]any{"description": "확정"}, "milestones": []any{}, "talent_id": winnerTalent,
	}, http.StatusCreated)
	loser.do(http.MethodPost, "/api/v1/quotes", map[string]any{
		"rfq_id": rfqID, "amount": 620_000, "currency": "KRW", "delivery_days": 10,
		"scope": map[string]any{"description": "대안"}, "milestones": []any{}, "talent_id": loserTalent,
	}, http.StatusCreated)

	quotes = buyer.do(http.MethodGet, "/api/v1/rfqs/"+rfqID+"/quotes", nil, http.StatusOK)
	var winningID, losingID string
	for _, raw := range quotes["items"].([]any) {
		item, _ := raw.(map[string]any)
		if item["amount"] == float64(450_000) {
			winningID = fmt.Sprint(item["id"])
			if item["talent_title"] == "" {
				t.Fatal("견적에 연결된 상품 이름이 없습니다")
			}
		} else {
			losingID = fmt.Sprint(item["id"])
		}
	}

	// Only the buyer who raised the request can choose.
	loser.do(http.MethodPost, "/api/v1/quotes/"+winningID+"/accept", map[string]any{}, http.StatusForbidden)

	result := buyer.do(http.MethodPost, "/api/v1/quotes/"+winningID+"/accept", map[string]any{"requirements": map[string]any{"범위": "합의된 대로"}}, http.StatusCreated)
	orderID := fmt.Sprint(result["order_id"])
	if result["amount"] != float64(450_000) {
		t.Fatalf("주문 금액=%v", result["amount"])
	}

	// The order carries the quoted price, not the product's list price.
	var amount int64
	var talentID, state string
	if err := pool.QueryRow(context.Background(), `SELECT amount,talent_id::text,state FROM orders WHERE id=$1`, orderID).Scan(&amount, &talentID, &state); err != nil {
		t.Fatalf("order query: %v", err)
	}
	if amount != 450_000 || talentID != winnerTalent || state != "CREATED" {
		t.Fatalf("주문=%d/%s/%s", amount, talentID, state)
	}

	var winningState, losingState, rfqState string
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT state FROM quotes WHERE id=$1),(SELECT state FROM quotes WHERE id=$2),(SELECT state FROM rfqs WHERE id=$3)`, winningID, losingID, rfqID).Scan(&winningState, &losingState, &rfqState); err != nil {
		t.Fatalf("state query: %v", err)
	}
	if winningState != "accepted" || losingState != "rejected" || rfqState != "awarded" {
		t.Fatalf("견적/요청 상태=%s/%s/%s", winningState, losingState, rfqState)
	}
	// The request is closed, so a second acceptance is refused.
	buyer.do(http.MethodPost, "/api/v1/quotes/"+losingID+"/accept", map[string]any{}, http.StatusConflict)

	// Both sellers hear the outcome through the normal notification pipeline.
	eventually(t, "낙찰 알림", 20*time.Second, func() bool {
		inbox := winner.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
		for _, raw := range inbox["items"].([]any) {
			if item, _ := raw.(map[string]any); item["event_type"] == "QuoteAccepted" {
				return true
			}
		}
		return false
	})
	eventually(t, "탈락 알림", 20*time.Second, func() bool {
		inbox := loser.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
		for _, raw := range inbox["items"].([]any) {
			if item, _ := raw.(map[string]any); item["event_type"] == "QuoteRejected" {
				return true
			}
		}
		return false
	})

	// The order behaves like any other from here on.
	buyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+orderID+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("quotepay")})
	if _, total := ledgerBalance(t, pool, orderID); total != 0 {
		t.Fatalf("견적 주문 결제 후 원장 합계=%d", total)
	}
}

func TestIntegrationRevisionRequestsAreCapped(t *testing.T) {
	server, pool := integrationServer(t)
	seller := registerSeller(t, server, "revseller")
	// The product promises two revisions, so the third request must be refused.
	talentID := createTalent(t, seller, uniqueName("수정 제한 상품"), "수정 횟수를 검증하는 상품입니다.", 40_000, 1, []any{})
	if _, err := pool.Exec(context.Background(), `UPDATE talents SET revision_count=2 WHERE id=$1`, talentID); err != nil {
		t.Fatalf("revision setup: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE talent_packages SET revision_count=2 WHERE talent_id=$1`, talentID); err != nil {
		t.Fatalf("package revision setup: %v", err)
	}
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("revbuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)

	for i := 1; i <= 2; i++ {
		buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/revision", map[string]any{"details": fmt.Sprintf("%d차 수정 요청", i), "priority": "normal", "attachments": []any{}}, http.StatusCreated)
		seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/deliveries", map[string]any{"delivery_type": "text", "content": map[string]any{"text": "재납품"}, "description": "재납품"}, http.StatusCreated)
	}
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/revision", map[string]any{"details": "3차 수정 요청", "priority": "normal", "attachments": []any{}}, http.StatusConflict)

	var revisions int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM revisions WHERE order_id=$1`, orderID).Scan(&revisions); err != nil {
		t.Fatalf("revision query: %v", err)
	}
	if revisions != 2 {
		t.Fatalf("기록된 수정 요청 %d건", revisions)
	}
	// The buyer still has a way out: confirm or dispute.
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "수정이 반영되지 않았습니다.", "evidence": []any{}}, http.StatusCreated)
}

func TestIntegrationPasswordChangeEndsOtherSessions(t *testing.T) {
	server, _ := integrationServer(t)
	name := uniqueName("pwuser")
	first := newClient(t, server.URL)
	first.register(name)

	// A second sign in stands for another device holding a live session.
	second := newClient(t, server.URL)
	second.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23"}, http.StatusOK)
	second.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)

	sessions := second.do(http.MethodGet, "/api/v1/me/sessions", nil, http.StatusOK)
	items, _ := sessions["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("활성 세션 %d개", len(items))
	}
	current := 0
	for _, raw := range items {
		if entry, _ := raw.(map[string]any); entry["current"] == true {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("현재 세션 표시 %d개", current)
	}

	second.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": "wrong-password", "new_password": "AnotherPass!456"}, http.StatusForbidden)
	second.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": "IntegrationPass!23", "new_password": "short"}, http.StatusBadRequest)
	second.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": "IntegrationPass!23", "new_password": "IntegrationPass!23"}, http.StatusBadRequest)

	result := second.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": "IntegrationPass!23", "new_password": "AnotherPass!456"}, http.StatusOK)
	if result["revoked_sessions"] != float64(1) {
		t.Fatalf("종료된 세션 수=%v", result["revoked_sessions"])
	}

	// Changing a password is how someone responds to a compromise, so the other
	// session must stop working while the current one continues.
	first.do(http.MethodGet, "/api/v1/me", nil, http.StatusUnauthorized)
	second.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)

	// The new password works and the old one does not.
	third := newClient(t, server.URL)
	third.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23"}, http.StatusUnauthorized)
	third.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "AnotherPass!456"}, http.StatusOK)

	// Signing out everywhere else is available without changing the password.
	revoked := second.do(http.MethodDelete, "/api/v1/me/sessions", nil, http.StatusOK)
	if revoked["revoked_sessions"] != float64(1) {
		t.Fatalf("일괄 종료된 세션 수=%v", revoked["revoked_sessions"])
	}
	third.do(http.MethodGet, "/api/v1/me", nil, http.StatusUnauthorized)
	second.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)
}

func TestIntegrationEditingAnApprovedTalentReturnsToReview(t *testing.T) {
	server, pool := integrationServer(t)
	operator := operatorClient(t, server, pool, "reviewop")
	seller := registerSeller(t, server, "editseller")
	keyword := uniqueName("검토복귀")
	talentID := createTalent(t, seller, keyword+" 상품", "승인 정책 검증용 상품 설명입니다.", 50_000, 1, []any{})

	// With a policy in force, a material edit to a live product has to be looked
	// at again instead of going straight out.
	policy := operator.do(http.MethodPost, "/api/v1/admin/approvals/policies", map[string]any{
		"resource_type": "talent_publish", "name": uniqueName("고액 검토"), "enabled": true, "priority": 10,
		"conditions": map[string]any{"min_amount": 100_000}, "steps": []map[string]any{{"role": "operator", "min_approvals": 1}},
	}, http.StatusCreated)
	// A policy with decision history cannot be deleted, so it is switched off
	// instead to keep it from affecting later tests.
	t.Cleanup(func() {
		operator.do(http.MethodPut, "/api/v1/admin/approvals/policies/"+fmt.Sprint(policy["id"]), map[string]any{
			"resource_type": "talent_publish", "name": "고액 검토 종료", "enabled": false, "priority": 10,
			"conditions": map[string]any{"min_amount": 100_000}, "steps": []map[string]any{{"role": "operator", "min_approvals": 1}},
		}, http.StatusOK)
	})

	// An edit below the threshold stays live: no policy covers it.
	minor := seller.do(http.MethodPut, "/api/v1/talents/"+talentID, talentPayload(keyword+" 상품 개정", "설명을 조금 다듬었습니다. 정책 임계값 아래입니다.", 60_000), http.StatusOK)
	if minor["status"] != "published" {
		t.Fatalf("임계값 아래 수정 후 상태=%v", minor["status"])
	}
	if found := seller.do(http.MethodGet, "/api/v1/talents?q="+keyword, nil, http.StatusOK); len(found["items"].([]any)) != 1 {
		t.Fatal("정책에 걸리지 않는 수정인데 검색에서 사라졌습니다")
	}

	// Raising the price past the threshold pulls it back into review.
	major := seller.do(http.MethodPut, "/api/v1/talents/"+talentID, talentPayload(keyword+" 프리미엄", "가격을 크게 올린 개정입니다. 검토가 필요합니다.", 500_000), http.StatusOK)
	if major["status"] != "review_pending" {
		t.Fatalf("고액 수정 후 상태=%v", major["status"])
	}
	if found := seller.do(http.MethodGet, "/api/v1/talents?q="+keyword, nil, http.StatusOK); len(found["items"].([]any)) != 0 {
		t.Fatal("검토 대기 상품이 검색에 남아 있습니다")
	}
	queue := operator.do(http.MethodGet, "/api/v1/admin/approvals/requests", nil, http.StatusOK)
	var requestID string
	for _, raw := range queue["items"].([]any) {
		if item, _ := raw.(map[string]any); item["resource_id"] == talentID {
			requestID = fmt.Sprint(item["id"])
		}
	}
	if requestID == "" {
		t.Fatal("수정 건이 승인 대기열에 없습니다")
	}
	operator.do(http.MethodPost, "/api/v1/admin/approvals/requests/"+requestID+"/decision", map[string]any{"decision": "approved", "note": "확인"}, http.StatusOK)
	if found := seller.do(http.MethodGet, "/api/v1/talents?q="+keyword, nil, http.StatusOK); len(found["items"].([]any)) != 1 {
		t.Fatal("승인 후에도 상품이 공개되지 않았습니다")
	}
}

// talentPayload builds a complete product body for an update.
func talentPayload(title, description string, price int64) map[string]any {
	return map[string]any{
		"title": title, "summary": title, "description": description,
		"service_type": "HUMAN", "base_price": price, "delivery_days": 3, "currency": "KRW", "revision_count": 1,
		"scope_included": []string{}, "scope_excluded": []string{}, "deliverables": []string{}, "tags": []string{"test"},
		"faq": []any{}, "refund_policy": "전액 환불", "instant_order": true, "quote_required": false, "subscription_enabled": false,
		"packages":     []map[string]any{{"package_type": "BASIC", "name": "기본", "description": "기본", "price": price, "delivery_days": 3, "revision_count": 1, "features": []string{}, "deliverables": []string{}, "sort_order": 0, "active": true}},
		"requirements": []map[string]any{{"label": "요구사항", "help_text": "", "field_type": "textarea", "required": true, "options": []any{}, "validation": map[string]any{}, "sort_order": 0}},
	}
}

func TestIntegrationIdentityListAndUnlinkGuard(t *testing.T) {
	server, pool := integrationServer(t)
	name := uniqueName("identity")
	user := newClient(t, server.URL)
	user.register(name)

	identities := user.do(http.MethodGet, "/api/v1/me/identities", nil, http.StatusOK)
	if items, _ := identities["items"].([]any); len(items) != 0 {
		t.Fatalf("연결된 계정 %d건", len(items))
	}
	if identities["has_password"] != true {
		t.Fatal("로컬 가입 계정은 비밀번호가 있어야 합니다")
	}

	// Seed a provider and a linked identity the way a completed OAuth sign in
	// would, so the unlink guard can be exercised without a live provider.
	background := context.Background()
	var providerID string
	if err := pool.QueryRow(background, `INSERT INTO auth_providers(id,slug,name,preset,provider_type,enabled,client_id) VALUES(gen_random_uuid(),$1,'검증 제공자','custom','oidc',true,'client') RETURNING id::text`, uniqueName("prov")).Scan(&providerID); err != nil {
		t.Fatalf("provider seed: %v", err)
	}
	var userID string
	if err := pool.QueryRow(background, `SELECT id::text FROM users WHERE username=$1`, name).Scan(&userID); err != nil {
		t.Fatalf("user query: %v", err)
	}
	if _, err := pool.Exec(background, `INSERT INTO external_identities(id,user_id,provider_id,subject) VALUES(gen_random_uuid(),$1::uuid,$2::uuid,'subject-1')`, userID, providerID); err != nil {
		t.Fatalf("identity seed: %v", err)
	}

	identities = user.do(http.MethodGet, "/api/v1/me/identities", nil, http.StatusOK)
	items, _ := identities["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("연결된 계정 %d건", len(items))
	}
	entry, _ := items[0].(map[string]any)
	identityID := fmt.Sprint(entry["id"])

	// With a password set the account keeps a way in, so unlinking is allowed.
	user.do(http.MethodDelete, "/api/v1/me/identities/"+identityID, nil, http.StatusNoContent)
	user.do(http.MethodDelete, "/api/v1/me/identities/"+identityID, nil, http.StatusNotFound)

	// A social only account must not be able to remove its last way in.
	if _, err := pool.Exec(background, `INSERT INTO external_identities(id,user_id,provider_id,subject) VALUES(gen_random_uuid(),$1::uuid,$2::uuid,'subject-2')`, userID, providerID); err != nil {
		t.Fatalf("identity seed: %v", err)
	}
	if _, err := pool.Exec(background, `UPDATE users SET password_hash=NULL WHERE id=$1::uuid`, userID); err != nil {
		t.Fatalf("password clear: %v", err)
	}
	identities = user.do(http.MethodGet, "/api/v1/me/identities", nil, http.StatusOK)
	if identities["has_password"] != false {
		t.Fatal("비밀번호를 지웠는데 있다고 보고합니다")
	}
	onlyItems, _ := identities["items"].([]any)
	onlyEntry, _ := onlyItems[0].(map[string]any)
	user.do(http.MethodDelete, "/api/v1/me/identities/"+fmt.Sprint(onlyEntry["id"]), nil, http.StatusConflict)

	// Setting a password again restores the choice.
	user.do(http.MethodPost, "/api/v1/me/password", map[string]any{"new_password": "RestoredPass!789"}, http.StatusOK)
	user.do(http.MethodDelete, "/api/v1/me/identities/"+fmt.Sprint(onlyEntry["id"]), nil, http.StatusNoContent)
}

// totpFor computes the code an authenticator app would show for a secret.
func totpFor(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		t.Fatalf("secret decode: %v", err)
	}
	return totpCode(decoded, uint64(at.Unix()/30))
}

func TestIntegrationTOTPCodeCannotBeReplayed(t *testing.T) {
	server, pool := integrationServer(t)
	name := uniqueName("mfauser")
	user := newClient(t, server.URL)
	user.register(name)

	setup := user.do(http.MethodPost, "/api/v1/me/mfa/totp/setup", map[string]any{}, http.StatusCreated)
	secret := fmt.Sprint(setup["secret"])
	user.do(http.MethodPost, "/api/v1/me/mfa/totp/confirm", map[string]any{"code": totpFor(t, secret, time.Now())}, http.StatusOK)
	if status := user.do(http.MethodGet, "/api/v1/me/mfa", nil, http.StatusOK); status["totp_enabled"] != true {
		t.Fatal("MFA가 활성화되지 않았습니다")
	}

	// The code just spent on confirmation must not also work as a sign in.
	replay := newClient(t, server.URL)
	replay.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23", "mfa_code": totpFor(t, secret, time.Now())}, http.StatusUnauthorized)

	// Clearing the spent step stands for the next window arriving, so the same
	// current code becomes usable exactly once.
	if _, err := pool.Exec(context.Background(), `UPDATE mfa_factors SET last_step=0 WHERE user_id=(SELECT id FROM users WHERE username=$1)`, name); err != nil {
		t.Fatalf("step reset: %v", err)
	}
	first := newClient(t, server.URL)
	first.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23", "mfa_code": totpFor(t, secret, time.Now())}, http.StatusOK)

	// Presenting the very same code again is refused even though it is still
	// inside its validity window.
	second := newClient(t, server.URL)
	second.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23", "mfa_code": totpFor(t, secret, time.Now())}, http.StatusUnauthorized)

	// A lost authenticator is recoverable by an operator instead of locking the
	// account out for ever.
	operator := operatorClient(t, server, pool, "mfaop")
	grantRole(t, pool, operatorUsername(t, pool, operator), "super_admin")
	var userID string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE username=$1`, name).Scan(&userID); err != nil {
		t.Fatalf("user query: %v", err)
	}
	operator.do(http.MethodDelete, "/api/v1/admin/users/"+userID+"/mfa", nil, http.StatusOK)
	operator.do(http.MethodDelete, "/api/v1/admin/users/"+userID+"/mfa", nil, http.StatusNotFound)
	after := newClient(t, server.URL)
	after.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23"}, http.StatusOK)
	// Resetting the factor also ends sessions that were opened with it.
	first.do(http.MethodGet, "/api/v1/me", nil, http.StatusUnauthorized)
}

// operatorUsername reads back the account name an operator client signed in as.
func operatorUsername(t *testing.T, pool *pgxpool.Pool, operator *client) string {
	t.Helper()
	me := operator.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)
	return fmt.Sprint(me["username"])
}

func TestIntegrationLocalLoginCannotBeTurnedOffWithoutAProvider(t *testing.T) {
	server, pool := integrationServer(t)
	admin := operatorClient(t, server, pool, "lockoutadmin")
	grantRole(t, pool, operatorUsername(t, pool, admin), "super_admin")
	background := context.Background()
	// Other tests may have left providers enabled, so this one establishes the
	// state its premise needs and puts it back afterwards.
	var previouslyEnabled []string
	rows, err := pool.Query(background, `SELECT id::text FROM auth_providers WHERE enabled`)
	if err != nil {
		t.Fatalf("provider query: %v", err)
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			previouslyEnabled = append(previouslyEnabled, id)
		}
	}
	rows.Close()
	if _, err := pool.Exec(background, `UPDATE auth_providers SET enabled=false`); err != nil {
		t.Fatalf("provider disable: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(background, `UPDATE system_settings SET value = value || '{"allow_local_login":true}'::jsonb WHERE key='auth.security'`)
		_, _ = pool.Exec(background, `DELETE FROM auth_providers WHERE slug LIKE 'lockout-%'`)
		for _, id := range previouslyEnabled {
			_, _ = pool.Exec(background, `UPDATE auth_providers SET enabled=true WHERE id=$1::uuid`, id)
		}
	})

	security := map[string]any{"session_ttl_hours": 12, "cookie_secure": false, "mfa_admin_required": false, "allow_local_login": false}

	// With nothing else to sign in with, this change would lock everyone out of
	// an offline deployment for good.
	admin.do(http.MethodPut, "/api/v1/admin/settings/auth.security", map[string]any{"value": security}, http.StatusConflict)
	var stillAllowed bool
	if err := pool.QueryRow(background, `SELECT (value->>'allow_local_login')::boolean FROM system_settings WHERE key='auth.security'`).Scan(&stillAllowed); err != nil {
		t.Fatalf("setting query: %v", err)
	}
	if !stillAllowed {
		t.Fatal("거부된 변경이 저장되었습니다")
	}

	// With a provider enabled the operator may turn local login off.
	slug := "lockout-" + uniqueName("p")
	provider := admin.do(http.MethodPost, "/api/v1/admin/auth-providers", map[string]any{
		"slug": slug, "name": "검증 제공자", "preset": "custom", "provider_type": "oidc", "enabled": true,
		"issuer_url": "https://issuer.example.test", "client_id": "client", "client_secret": "secret",
		"scopes": []string{"openid", "email"}, "claim_mapping": map[string]any{}, "options": map[string]any{},
	}, http.StatusCreated)
	admin.do(http.MethodPut, "/api/v1/admin/settings/auth.security", map[string]any{"value": security}, http.StatusOK)

	// Now the provider is the only way in, so it cannot be disabled or removed
	// until local login comes back.
	providerID := fmt.Sprint(provider["id"])
	admin.do(http.MethodPut, "/api/v1/admin/auth-providers/"+providerID, map[string]any{
		"slug": slug, "name": "검증 제공자", "preset": "custom", "provider_type": "oidc", "enabled": false,
		"issuer_url": "https://issuer.example.test", "client_id": "client",
		"scopes": []string{"openid", "email"}, "claim_mapping": map[string]any{}, "options": map[string]any{},
	}, http.StatusConflict)
	admin.do(http.MethodDelete, "/api/v1/admin/auth-providers/"+providerID, nil, http.StatusConflict)

	// Restoring local login releases the guard.
	security["allow_local_login"] = true
	admin.do(http.MethodPut, "/api/v1/admin/settings/auth.security", map[string]any{"value": security}, http.StatusOK)
	admin.do(http.MethodDelete, "/api/v1/admin/auth-providers/"+providerID, nil, http.StatusNoContent)
}

func TestIntegrationOrderOptionsArePricedByTheProduct(t *testing.T) {
	server, pool := integrationServer(t)
	seller := registerSeller(t, server, "optionseller")
	body := talentPayload(uniqueName("옵션 상품"), "추가 옵션 가격 검증용 상품입니다.", 100_000)
	body["options"] = []map[string]any{
		{"name": "급행 작업", "description": "2일 단축", "price": 30_000, "additional_days": 0, "sort_order": 0, "active": true},
		{"name": "추가 리비전", "description": "1회 더", "price": 10_000, "additional_days": 2, "sort_order": 1, "active": true},
	}
	created := seller.do(http.MethodPost, "/api/v1/talents", body, http.StatusCreated)
	talentID := fmt.Sprint(created["id"])
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)

	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("optionbuyer"))
	detail := buyer.do(http.MethodGet, "/api/v1/talents/"+talentID, nil, http.StatusOK)
	options, _ := detail["options"].([]any)
	if len(options) != 2 {
		t.Fatalf("상품 옵션 %d개", len(options))
	}
	var rushID, revisionID string
	for _, raw := range options {
		item, _ := raw.(map[string]any)
		if item["name"] == "급행 작업" {
			rushID = fmt.Sprint(item["id"])
		} else {
			revisionID = fmt.Sprint(item["id"])
		}
	}

	// A price sent by the client is ignored: the stored option decides.
	order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"},
		"options": []map[string]any{{"id": rushID, "price": 1}, {"id": revisionID, "price": 1}},
	}, http.StatusCreated)
	if order["amount"] != float64(140_000) {
		t.Fatalf("옵션 포함 주문 금액=%v", order["amount"])
	}
	// The option's extra days extend the promised due date.
	var due, created2 time.Time
	if err := pool.QueryRow(context.Background(), `SELECT due_at,created_at FROM orders WHERE id=$1`, fmt.Sprint(order["id"])).Scan(&due, &created2); err != nil {
		t.Fatalf("order query: %v", err)
	}
	if due.Sub(created2) < 4*24*time.Hour {
		t.Fatalf("납기가 옵션 추가일을 반영하지 않았습니다: %s", due.Sub(created2))
	}

	// An option that does not belong to the product cannot be attached.
	buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"},
		"options": []map[string]any{{"id": uuid.NewString(), "price": 0}},
	}, http.StatusBadRequest)
	// Neither can a bare price with no option identifier.
	buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"},
		"options": []map[string]any{{"price": 500_000}},
	}, http.StatusBadRequest)
}

func TestIntegrationSellerCapacityStopsNewOrders(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "capseller", uniqueName("작업량 상품"), 30_000)
	background := context.Background()
	if _, err := pool.Exec(background, `UPDATE seller_profiles SET capacity=2 WHERE user_id=(SELECT seller_id FROM talents WHERE id=$1)`, talentID); err != nil {
		t.Fatalf("capacity setup: %v", err)
	}
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("capbuyer"))

	var firstID string
	for i := 0; i < 2; i++ {
		order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
			"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{},
		}, http.StatusCreated)
		if i == 0 {
			firstID = fmt.Sprint(order["id"])
		}
	}

	// A seller who declared they can carry two jobs must not be handed a third.
	buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{},
	}, http.StatusConflict)

	// Buyers see the state before they reach checkout rather than after.
	detail := buyer.do(http.MethodGet, "/api/v1/talents/"+talentID, nil, http.StatusOK)
	if detail["accepting_orders"] != false {
		t.Fatalf("상세의 주문 가능 여부=%v", detail["accepting_orders"])
	}

	// Finishing one frees a slot again.
	operator := operatorClient(t, server, pool, "capop")
	operator.do(http.MethodPost, "/api/v1/orders/"+firstID+"/transition", map[string]any{"to": "CANCELLED", "note": "정리"}, http.StatusOK)
	buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{},
	}, http.StatusCreated)

	// A seller who never set a limit is not restricted.
	other, otherTalent, _ := sellTalent(t, server, "uncappedseller", uniqueName("무제한 상품"), 10_000)
	if _, err := pool.Exec(background, `UPDATE seller_profiles SET capacity=0 WHERE user_id=(SELECT seller_id FROM talents WHERE id=$1)`, otherTalent); err != nil {
		t.Fatalf("capacity setup: %v", err)
	}
	for i := 0; i < 3; i++ {
		buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{
			"talent_id": otherTalent, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{},
		}, http.StatusCreated)
	}
	_ = other
	_ = seller
}

func TestIntegrationCompletedSettlementClearsTheLiability(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "payoutseller", uniqueName("지급 원장 상품"), 200_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("payoutbuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)

	balances, total := ledgerBalance(t, pool, orderID)
	if total != 0 || balances["Seller Payable"] != -180_000 {
		t.Fatalf("구매확정 후 원장=%v (합계 %d)", balances, total)
	}

	// Marking a settlement paid used to touch no ledger at all, so the payable
	// stood for ever as a liability for money that had already gone out.
	operator := operatorClient(t, server, pool, "payoutop")
	settlements := seller.do(http.MethodGet, "/api/v1/me/settlements", nil, http.StatusOK)
	entry, _ := settlements["items"].([]any)[0].(map[string]any)
	operator.do(http.MethodPost, "/api/v1/admin/settlements/"+fmt.Sprint(entry["id"])+"/action", map[string]any{"action": "complete"}, http.StatusOK)

	balances, total = ledgerBalance(t, pool, orderID)
	if total != 0 {
		t.Fatalf("지급 후 원장 합계=%d (%v)", total, balances)
	}
	if balances["Seller Payable"] != 0 {
		t.Fatalf("지급 후에도 판매자 미지급금이 %d원 남았습니다", -balances["Seller Payable"])
	}
	if balances["Seller Payout"] != -180_000 {
		t.Fatalf("지급 계정 잔액=%d", balances["Seller Payout"])
	}
	eventually(t, "지급 완료 알림", 20*time.Second, func() bool {
		inbox := seller.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)
		for _, raw := range inbox["items"].([]any) {
			if item, _ := raw.(map[string]any); item["event_type"] == "SettlementPaid" {
				return true
			}
		}
		return false
	})
}

func TestIntegrationAIUsageReportsSpendAgainstTheBudget(t *testing.T) {
	server, pool := integrationServer(t)
	operator := operatorClient(t, server, pool, "aiop")
	grantRole(t, pool, operatorUsername(t, pool, operator), "super_admin")
	background := context.Background()

	empty := operator.do(http.MethodGet, "/api/v1/admin/ai/usage", nil, http.StatusOK)
	if empty["budget_enforced"] != false || empty["month_spent"] != float64(0) {
		t.Fatalf("사용 전 리포트=%v", empty)
	}

	// A budget with no price list cannot be measured, so both are configured
	// the way an operator would before enabling the gateway.
	if _, err := pool.Exec(background, `UPDATE system_settings SET value = value || '{"monthly_budget":1,"currency":"USD","pricing":{"gpt-test":{"input_per_1k":0.001,"output_per_1k":0.002}}}'::jsonb WHERE key='ai.gateway'`); err != nil {
		t.Fatalf("gateway setting: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(background, `UPDATE system_settings SET value = value || '{"monthly_budget":0}'::jsonb WHERE key='ai.gateway'`)
		_, _ = pool.Exec(background, `DELETE FROM ai_usage WHERE model='gpt-test'`)
	})

	// Stand in for two completed gateway calls this month.
	for _, tokens := range [][2]int{{100_000, 50_000}, {200_000, 100_000}} {
		if _, err := pool.Exec(background, `INSERT INTO ai_usage(id,feature,model,input_tokens,output_tokens,estimated_cost) VALUES(gen_random_uuid(),'talent_draft','gpt-test',$1,$2,$3)`,
			tokens[0], tokens[1], float64(tokens[0])/1000*0.001+float64(tokens[1])/1000*0.002); err != nil {
			t.Fatalf("usage seed: %v", err)
		}
	}

	report := operator.do(http.MethodGet, "/api/v1/admin/ai/usage", nil, http.StatusOK)
	// 0.2 + 0.4 spent against a budget of 1.
	if spent, _ := report["month_spent"].(float64); spent < 0.5999 || spent > 0.6001 {
		t.Fatalf("이번 달 사용액=%v", report["month_spent"])
	}
	if report["budget_enforced"] != true || report["exhausted"] != false {
		t.Fatalf("예산 상태=%v", report)
	}
	if remaining, _ := report["remaining"].(float64); remaining < 0.3999 || remaining > 0.4001 {
		t.Fatalf("잔여 예산=%v", report["remaining"])
	}
	if report["executions"] != float64(2) {
		t.Fatalf("호출 수=%v", report["executions"])
	}
	breakdown, _ := report["breakdown"].([]any)
	if len(breakdown) != 1 {
		t.Fatalf("분류 %d건", len(breakdown))
	}
	entry, _ := breakdown[0].(map[string]any)
	if entry["feature"] != "talent_draft" || entry["model"] != "gpt-test" || entry["calls"] != float64(2) {
		t.Fatalf("분류 항목=%v", entry)
	}

	// Passing the budget flips the flag an operator watches, and the draft
	// endpoint keeps working by falling back to its local template.
	if _, err := pool.Exec(background, `INSERT INTO ai_usage(id,feature,model,input_tokens,output_tokens,estimated_cost) VALUES(gen_random_uuid(),'talent_draft','gpt-test',0,0,0.5)`); err != nil {
		t.Fatalf("usage seed: %v", err)
	}
	report = operator.do(http.MethodGet, "/api/v1/admin/ai/usage", nil, http.StatusOK)
	if report["exhausted"] != true || report["remaining"] != float64(0) {
		t.Fatalf("소진 상태=%v", report)
	}
	seller := registerSeller(t, server, "aiseller")
	draft := seller.do(http.MethodPost, "/api/v1/ai/talents/draft", map[string]any{"idea": "예산이 소진된 상태에서도 상품 초안이 필요합니다.", "locale": "ko-KR"}, http.StatusOK)
	meta, _ := draft["meta"].(map[string]any)
	if meta["mode"] != "offline_template" {
		t.Fatalf("초안 생성 방식=%v", meta["mode"])
	}
	if draft["draft"] == nil {
		t.Fatal("예산이 소진되어도 초안은 반환되어야 합니다")
	}
}

func TestIntegrationFeatureFlagsGateTheirEndpoints(t *testing.T) {
	server, pool := integrationServer(t)
	operator := operatorClient(t, server, pool, "flagop")
	grantRole(t, pool, operatorUsername(t, pool, operator), "super_admin")
	background := context.Background()
	t.Cleanup(func() {
		_, _ = pool.Exec(background, `UPDATE feature_flags SET enabled=true WHERE key IN ('smart_quote','enterprise','ai_matching','agent_marketplace')`)
	})

	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("flagbuyer"))
	buyer.do(http.MethodGet, "/api/v1/rfqs", nil, http.StatusOK)
	buyer.do(http.MethodGet, "/api/v1/me/organizations", nil, http.StatusOK)

	flags := operator.do(http.MethodGet, "/api/v1/admin/feature-flags", nil, http.StatusOK)
	if items, _ := flags["items"].([]any); len(items) != 4 {
		t.Fatalf("기능 플래그 %d개", len(items))
	}
	// A switch offered for a feature that does not exist is worse than none.
	operator.do(http.MethodPut, "/api/v1/admin/feature-flags/subscription", map[string]any{"enabled": true}, http.StatusNotFound)

	operator.do(http.MethodPut, "/api/v1/admin/feature-flags/smart_quote", map[string]any{"enabled": false}, http.StatusOK)
	// Hiding a menu is not enough: the endpoint itself has to refuse.
	buyer.do(http.MethodGet, "/api/v1/rfqs", nil, http.StatusNotFound)
	buyer.do(http.MethodPost, "/api/v1/rfqs", map[string]any{
		"title": uniqueName("막힌 프로젝트"), "description": "기능이 꺼진 상태의 요청입니다.", "requirements": map[string]any{}, "currency": "KRW",
	}, http.StatusNotFound)
	// Unrelated features keep working.
	buyer.do(http.MethodGet, "/api/v1/me/organizations", nil, http.StatusOK)

	published := buyer.do(http.MethodGet, "/api/v1/features", nil, http.StatusOK)
	feature, _ := published["features"].(map[string]any)
	if feature["smart_quote"] != false || feature["enterprise"] != true {
		t.Fatalf("공개 기능 목록=%v", feature)
	}

	operator.do(http.MethodPut, "/api/v1/admin/feature-flags/enterprise", map[string]any{"enabled": false}, http.StatusOK)
	buyer.do(http.MethodGet, "/api/v1/me/organizations", nil, http.StatusNotFound)
	buyer.do(http.MethodPost, "/api/v1/organizations", map[string]any{"name": uniqueName("막힌 조직")}, http.StatusNotFound)

	// Switching back on restores the feature within the cache window.
	operator.do(http.MethodPut, "/api/v1/admin/feature-flags/smart_quote", map[string]any{"enabled": true}, http.StatusOK)
	operator.do(http.MethodPut, "/api/v1/admin/feature-flags/enterprise", map[string]any{"enabled": true}, http.StatusOK)
	buyer.do(http.MethodGet, "/api/v1/rfqs", nil, http.StatusOK)
	buyer.do(http.MethodGet, "/api/v1/me/organizations", nil, http.StatusOK)
}

// uploadOrderFile posts a small file to an order and returns the status code.
func uploadOrderFile(t *testing.T, caller *client, orderID, name string, size int) int {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("k"), size)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	writer.Close()
	request, _ := http.NewRequest(http.MethodPost, caller.base+"/api/v1/orders/"+orderID+"/files", &buffer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := caller.http.Do(request)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func TestIntegrationOrderFileUploadsAreCapped(t *testing.T) {
	server, pool := integrationServer(t)
	background := context.Background()
	// Three files per order and a tiny daily allowance stand in for the real
	// ceilings so the limits can be reached without moving real volume.
	if _, err := pool.Exec(background, `UPDATE system_settings SET value = value || '{"max_files_per_order":3,"daily_upload_mb_per_user":1}'::jsonb WHERE key='storage.policy'`); err != nil {
		t.Fatalf("policy setup: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(background, `UPDATE system_settings SET value = value || '{"max_files_per_order":100,"daily_upload_mb_per_user":500}'::jsonb WHERE key='storage.policy'`)
	})

	seller, talentID, _ := sellTalent(t, server, "fileseller", uniqueName("첨부 상한 상품"), 20_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("filebuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)

	for i := 0; i < 3; i++ {
		if status := uploadOrderFile(t, buyer, orderID, fmt.Sprintf("note-%d.txt", i), 1024); status != http.StatusCreated {
			t.Fatalf("%d번째 업로드 상태=%d", i+1, status)
		}
	}
	// The order is full even though every file was well inside the size limit.
	if status := uploadOrderFile(t, buyer, orderID, "overflow.txt", 1024); status != http.StatusTooManyRequests {
		t.Fatalf("상한 초과 업로드 상태=%d", status)
	}

	// A different order is unaffected by another order's count.
	secondOrder := payAndDeliver(t, buyer, seller, talentID)
	if status := uploadOrderFile(t, buyer, secondOrder, "fresh.txt", 1024); status != http.StatusCreated {
		t.Fatalf("다른 주문 업로드 상태=%d", status)
	}

	// The daily volume ceiling stops one account regardless of which order it
	// uploads to.
	if status := uploadOrderFile(t, buyer, secondOrder, "big.bin", 1_100_000); status != http.StatusTooManyRequests {
		t.Fatalf("일일 용량 초과 상태=%d", status)
	}
	// The seller has their own allowance.
	if status := uploadOrderFile(t, seller, secondOrder, "seller.txt", 1024); status != http.StatusCreated {
		t.Fatalf("판매자 업로드 상태=%d", status)
	}
}

func TestIntegrationOrganizationManagersCanOpenOrdersTheyFunded(t *testing.T) {
	server, pool := integrationServer(t)
	owner := newClient(t, server.URL)
	owner.register(uniqueName("visibilityowner"))
	memberName := uniqueName("visibilitymember")
	member := newClient(t, server.URL)
	member.register(memberName)
	organization := owner.do(http.MethodPost, "/api/v1/organizations", map[string]any{"name": uniqueName("가시성 검증사")}, http.StatusCreated)
	orgID := fmt.Sprint(organization["id"])
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/members", map[string]any{"account": memberName, "role": "member"}, http.StatusCreated)

	seller, talentID, _ := sellTalent(t, server, "visibilityseller", uniqueName("가시성 상품"), 50_000)
	order := member.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}, "organization_id": orgID,
	}, http.StatusCreated)
	orderID := fmt.Sprint(order["id"])

	// The owner is accountable for this spend, so the order must be inspectable
	// and not merely listed.
	detail := owner.do(http.MethodGet, "/api/v1/orders/"+orderID, nil, http.StatusOK)
	if detail["order_number"] == nil || detail["timeline"] == nil {
		t.Fatalf("조직 관리자가 본 주문=%v", detail)
	}
	owner.do(http.MethodGet, "/api/v1/orders/"+orderID+"/disputes", nil, http.StatusOK)

	// The conversation stays between the two parties.
	owner.do(http.MethodGet, "/api/v1/orders/"+orderID+"/messages", nil, http.StatusForbidden)

	// Someone outside the organization sees nothing, and a plain member sees
	// only their own order rather than everyone's.
	outsider := newClient(t, server.URL)
	outsider.register(uniqueName("visibilityoutsider"))
	outsider.do(http.MethodGet, "/api/v1/orders/"+orderID, nil, http.StatusNotFound)
	member.do(http.MethodGet, "/api/v1/orders/"+orderID, nil, http.StatusOK)

	// A personal order by the same member is not the organization's business.
	personal := member.do(http.MethodPost, "/api/v1/orders", map[string]any{
		"talent_id": talentID, "requirements": map[string]any{"요구사항": "개인"}, "options": []any{},
	}, http.StatusCreated)
	owner.do(http.MethodGet, "/api/v1/orders/"+fmt.Sprint(personal["id"]), nil, http.StatusNotFound)
	_ = seller
	_ = pool
}

// TestIntegrationAdminListsPageInsteadOfTruncating guards the operator console
// against silent truncation: a list that stops at a fixed ceiling looks exactly
// like a list that has nothing more in it.
func TestIntegrationAdminListsPageInsteadOfTruncating(t *testing.T) {
	server, pool := integrationServer(t)
	// These three lists sit behind different permissions, so the check runs as
	// an account that holds all of them.
	operatorName := uniqueName("pagingop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)
	// Give every list something to page over.
	for i := 0; i < 4; i++ {
		seller, talentID, _ := sellTalent(t, server, fmt.Sprintf("pagingseller%d", i), uniqueName("페이징 상품"), 30_000)
		buyer := newClient(t, server.URL)
		buyer.register(uniqueName("pagingbuyer"))
		orderID := payAndDeliver(t, buyer, seller, talentID)
		buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)
	}

	for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/talents", "/api/v1/admin/settlements"} {
		whole := operator.do(http.MethodGet, path+"?limit=500", nil, http.StatusOK)
		expected := len(whole["items"].([]any))
		if expected < 4 {
			t.Fatalf("%s 전체 %d건, 페이징을 검증할 만큼 쌓이지 않았습니다", path, expected)
		}
		seen := map[string]bool{}
		cursor, pages := "", 0
		for {
			target := path + "?limit=3"
			if cursor != "" {
				target += "&cursor=" + url.QueryEscape(cursor)
			}
			page := operator.do(http.MethodGet, target, nil, http.StatusOK)
			rows := page["items"].([]any)
			for _, row := range rows {
				id := fmt.Sprint(row.(map[string]any)["id"])
				if seen[id] {
					t.Fatalf("%s 커서 페이징이 %s를 두 번 돌려줬습니다", path, id)
				}
				seen[id] = true
			}
			pages++
			if pages > expected+5 {
				t.Fatalf("%s 페이징이 끝나지 않습니다", path)
			}
			next, ok := page["next_cursor"].(string)
			if !ok || next == "" {
				break
			}
			if len(rows) != 3 {
				t.Fatalf("%s 다음 커서를 주면서 %d건만 돌려줬습니다", path, len(rows))
			}
			cursor = next
		}
		if len(seen) < expected {
			t.Fatalf("%s 한 번에 %d건인데 페이징으로는 %d건만 나왔습니다", path, expected, len(seen))
		}
	}
}

// TestIntegrationOrderHistoryReachesPastTheFirstPage covers what a flat LIMIT
// hid: a buyer's older orders were unreachable through the product entirely.
func TestIntegrationOrderHistoryReachesPastTheFirstPage(t *testing.T) {
	server, pool := integrationServer(t)
	_, talentID, _ := sellTalent(t, server, "historyseller", uniqueName("이력 상품"), 20_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("historybuyer"))
	placed := make(map[string]bool)
	for i := 0; i < 5; i++ {
		order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
		placed[fmt.Sprint(order["id"])] = true
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		target := "/api/v1/orders?limit=2"
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		page := buyer.do(http.MethodGet, target, nil, http.StatusOK)
		for _, row := range page["items"].([]any) {
			id := fmt.Sprint(row.(map[string]any)["id"])
			if seen[id] {
				t.Fatalf("주문 %s가 두 번 나왔습니다", id)
			}
			seen[id] = true
		}
		next, ok := page["next_cursor"].(string)
		if !ok || next == "" {
			break
		}
		cursor = next
	}
	for id := range placed {
		if !seen[id] {
			t.Fatalf("주문 %s가 페이징 결과에 없습니다", id)
		}
	}
	if len(seen) != len(placed) {
		t.Fatalf("내 주문 %d건인데 %d건이 나왔습니다", len(placed), len(seen))
	}

	// Filters narrow the same list rather than reaching a different one.
	byRole := buyer.do(http.MethodGet, "/api/v1/orders?role=buyer&limit=50", nil, http.StatusOK)["items"].([]any)
	if len(byRole) != len(placed) {
		t.Fatalf("구매 필터 결과=%d건", len(byRole))
	}
	if sold := buyer.do(http.MethodGet, "/api/v1/orders?role=seller&limit=50", nil, http.StatusOK)["items"].([]any); len(sold) != 0 {
		t.Fatalf("판매 필터에 %d건이 나옵니다", len(sold))
	}
	if open := buyer.do(http.MethodGet, "/api/v1/orders?state=OPEN&limit=50", nil, http.StatusOK)["items"].([]any); len(open) != len(placed) {
		t.Fatalf("진행 중 필터=%d건", len(open))
	}
	if done := buyer.do(http.MethodGet, "/api/v1/orders?state=COMPLETED&limit=50", nil, http.StatusOK)["items"].([]any); len(done) != 0 {
		t.Fatalf("완료 필터에 %d건이 나옵니다", len(done))
	}
	// An unknown state is ignored rather than reaching the query.
	if all := buyer.do(http.MethodGet, "/api/v1/orders?state=NOT_A_STATE&limit=50", nil, http.StatusOK)["items"].([]any); len(all) != len(placed) {
		t.Fatalf("알 수 없는 상태 필터가 목록을 바꿨습니다: %d건", len(all))
	}

	// The list is still scoped to the caller, and an operator still sees orders
	// they are not a party to.
	stranger := newClient(t, server.URL)
	stranger.register(uniqueName("historystranger"))
	if rows := stranger.do(http.MethodGet, "/api/v1/orders", nil, http.StatusOK)["items"].([]any); len(rows) != 0 {
		t.Fatalf("남의 주문 %d건이 보입니다", len(rows))
	}
	operator := operatorClient(t, server, pool, "historyop")
	if rows := operator.do(http.MethodGet, "/api/v1/orders?limit=200", nil, http.StatusOK)["items"].([]any); len(rows) < len(placed) {
		t.Fatalf("운영자에게 주문이 %d건만 보입니다", len(rows))
	}
}

// TestIntegrationRFQVisibilityAndPaging pins the three audiences of the quote
// board after the role bypasses moved from SQL parameters into Go, and covers
// the older requests that a flat ceiling used to hide.
func TestIntegrationRFQVisibilityAndPaging(t *testing.T) {
	server, pool := integrationServer(t)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("rfqbuyer"))
	mine := map[string]bool{}
	for i := 0; i < 4; i++ {
		rfq := buyer.do(http.MethodPost, "/api/v1/rfqs", map[string]any{
			"title": uniqueName("견적 프로젝트"), "description": "요구사항 설명입니다.", "requirements": map[string]any{},
			"budget_min": 100_000, "budget_max": 500_000, "currency": "KRW",
		}, http.StatusCreated)
		mine[fmt.Sprint(rfq["id"])] = true
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		target := "/api/v1/rfqs?limit=2"
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		page := buyer.do(http.MethodGet, target, nil, http.StatusOK)
		for _, row := range page["items"].([]any) {
			seen[fmt.Sprint(row.(map[string]any)["id"])] = true
		}
		next, ok := page["next_cursor"].(string)
		if !ok || next == "" {
			break
		}
		cursor = next
	}
	for id := range mine {
		if !seen[id] {
			t.Fatalf("내 견적 요청 %s가 페이징 결과에 없습니다", id)
		}
	}

	// A buyer who did not post them sees nothing; a seller sees open requests
	// because that is the point of the board; an operator sees everything.
	otherBuyer := newClient(t, server.URL)
	otherBuyer.register(uniqueName("rfqotherbuyer"))
	if rows := otherBuyer.do(http.MethodGet, "/api/v1/rfqs", nil, http.StatusOK)["items"].([]any); len(rows) != 0 {
		t.Fatalf("남의 견적 요청 %d건이 구매자에게 보입니다", len(rows))
	}
	seller, _, _ := sellTalent(t, server, "rfqseller", uniqueName("견적 상품"), 40_000)
	sellerRows := seller.do(http.MethodGet, "/api/v1/rfqs?limit=200", nil, http.StatusOK)["items"].([]any)
	if len(sellerRows) < len(mine) {
		t.Fatalf("판매자에게 공개 견적이 %d건만 보입니다", len(sellerRows))
	}
	operator := operatorClient(t, server, pool, "rfqop")
	if rows := operator.do(http.MethodGet, "/api/v1/rfqs?limit=200", nil, http.StatusOK)["items"].([]any); len(rows) < len(mine) {
		t.Fatalf("운영자에게 견적이 %d건만 보입니다", len(rows))
	}
}

// TestIntegrationReviewsArePublished covers the gap where reviews were written
// and scored but never shown to anyone.
func TestIntegrationReviewsArePublished(t *testing.T) {
	server, _ := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "reviewseller", uniqueName("후기 상품"), 60_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("reviewbuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/review", map[string]any{
		"quality": 5, "communication": 4, "timeliness": 5, "professionalism": 3,
		"repurchase": true, "body": "설명이 꼼꼼했고 납기도 정확했습니다.",
	}, http.StatusCreated)

	// Anyone can read the review, including someone who is not signed in.
	anonymous := newClient(t, server.URL)
	page := anonymous.do(http.MethodGet, "/api/v1/talents/"+talentID+"/reviews", nil, http.StatusOK)
	rows := page["items"].([]any)
	if len(rows) != 1 {
		t.Fatalf("공개된 후기 %d건", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["body"] != "설명이 꼼꼼했고 납기도 정확했습니다." {
		t.Fatalf("후기 본문이 다릅니다: %v", first["body"])
	}
	if first["average"].(float64) != 4.25 {
		t.Fatalf("평균 점수=%v", first["average"])
	}
	// The reviewer's full name is not published with it.
	name, _ := first["buyer_name"].(string)
	if name == "" || !strings.Contains(name, "*") {
		t.Fatalf("구매자 이름이 가려지지 않았습니다: %q", name)
	}
	summary, ok := page["summary"].(map[string]any)
	if !ok || summary["count"].(float64) != 1 {
		t.Fatalf("요약=%v", page["summary"])
	}
	if distribution, ok := summary["distribution"].([]any); !ok || len(distribution) != 5 || distribution[3].(float64) != 1 {
		t.Fatalf("점수 분포=%v", summary["distribution"])
	}

	// An unpublished talent does not expose a review page.
	draft := seller.do(http.MethodPost, "/api/v1/talents", talentPayload(uniqueName("비공개 상품"), "비공개 상품 상세 설명입니다.", 10_000), http.StatusCreated)
	anonymous.do(http.MethodGet, "/api/v1/talents/"+fmt.Sprint(draft["id"])+"/reviews", nil, http.StatusNotFound)
}

// TestIntegrationCategoryBrowsing covers navigation that used to be eight
// hardcoded words fed into the text search.
func TestIntegrationCategoryBrowsing(t *testing.T) {
	server, _ := integrationServer(t)
	browser := newClient(t, server.URL)
	catalog := browser.do(http.MethodGet, "/api/v1/categories", nil, http.StatusOK)["items"].([]any)
	if len(catalog) < 8 {
		t.Fatalf("카테고리가 %d개뿐입니다", len(catalog))
	}
	bySlug := map[string]map[string]any{}
	for _, row := range catalog {
		item := row.(map[string]any)
		bySlug[fmt.Sprint(item["slug"])] = item
	}
	child, ok := bySlug["design-brand"]
	if !ok || child["parent_id"] == nil {
		t.Fatalf("하위 카테고리가 없습니다: %v", child)
	}

	// A listing filed under a child category is found by browsing its parent,
	// even though its text never contains the parent's name.
	seller := newClient(t, server.URL)
	seller.register(uniqueName("categoryseller"))
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "브랜딩", "biography": "", "skills": []string{"logo"}, "capacity": 5, "settings": map[string]any{},
	}, http.StatusOK)
	title := uniqueName("로고 제작")
	payload := talentPayload(title, "브랜드 아이덴티티를 정리해 드립니다.", 300_000)
	payload["category_id"] = child["id"]
	talent := seller.do(http.MethodPost, "/api/v1/talents", payload, http.StatusCreated)
	talentID := fmt.Sprint(talent["id"])
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)

	found := func(query string) bool {
		rows := browser.do(http.MethodGet, "/api/v1/talents?limit=100&"+query, nil, http.StatusOK)["items"].([]any)
		for _, row := range rows {
			if fmt.Sprint(row.(map[string]any)["id"]) == talentID {
				return true
			}
		}
		return false
	}
	if !found("category=design") {
		t.Fatal("상위 카테고리로 둘러볼 때 하위 상품이 나오지 않습니다")
	}
	if !found("category=design-brand") {
		t.Fatal("직접 지정한 카테고리로 찾지 못했습니다")
	}
	if found("category=development") {
		t.Fatal("다른 카테고리에서 상품이 보입니다")
	}
	// The detail response carries the category so the page can show it.
	detail := browser.do(http.MethodGet, "/api/v1/talents/"+talentID, nil, http.StatusOK)
	category, ok := detail["category"].(map[string]any)
	if !ok || category["slug"] != "design-brand" {
		t.Fatalf("상품 상세의 카테고리=%v", detail["category"])
	}
}

// TestIntegrationCatalogueFiltersAndPaging covers browsing that used to stop at
// the first page with no way to narrow what it showed.
func TestIntegrationCatalogueFiltersAndPaging(t *testing.T) {
	server, _ := integrationServer(t)
	browser := newClient(t, server.URL)
	marker := uniqueName("필터검증")
	prices := []int64{10_000, 200_000, 900_000}
	ids := make([]string, 0, len(prices))
	for i, price := range prices {
		seller, talentID, _ := sellTalent(t, server, fmt.Sprintf("filterseller%d", i), marker+fmt.Sprint(i), price)
		_ = seller
		ids = append(ids, talentID)
	}

	search := func(query string) map[string]any {
		return browser.do(http.MethodGet, "/api/v1/talents?q="+url.QueryEscape(marker)+"&"+query, nil, http.StatusOK)
	}
	titles := func(page map[string]any) []string {
		out := []string{}
		for _, row := range page["items"].([]any) {
			out = append(out, fmt.Sprint(row.(map[string]any)["id"]))
		}
		return out
	}

	if got := titles(search("price_max=100000")); len(got) != 1 || got[0] != ids[0] {
		t.Fatalf("가격 상한 필터 결과=%v", got)
	}
	if got := titles(search("price_min=150000")); len(got) != 2 {
		t.Fatalf("가격 하한 필터 결과=%v", got)
	}
	if got := titles(search("sort=price_asc")); len(got) != 3 || got[0] != ids[0] || got[2] != ids[2] {
		t.Fatalf("가격 오름차순 정렬=%v", got)
	}
	if got := titles(search("sort=price_desc")); len(got) != 3 || got[0] != ids[2] {
		t.Fatalf("가격 내림차순 정렬=%v", got)
	}
	if got := titles(search("service_type=AI")); len(got) != 0 {
		t.Fatalf("AI 상품이 없는데 %v건 나왔습니다", got)
	}
	if got := titles(search("max_delivery_days=1")); len(got) != 0 {
		t.Fatalf("납기 1일 상품이 없는데 %v건 나왔습니다", got)
	}

	// Paging reports whether there is more and never repeats a row.
	first := search("sort=price_asc&limit=2")
	if first["has_more"] != true || first["next_offset"].(float64) != 2 {
		t.Fatalf("첫 페이지 메타=%v %v", first["has_more"], first["next_offset"])
	}
	second := search("sort=price_asc&limit=2&offset=2")
	if second["has_more"] != false {
		t.Fatalf("마지막 페이지가 더 있다고 답합니다")
	}
	seen := map[string]bool{}
	for _, id := range append(titles(first), titles(second)...) {
		if seen[id] {
			t.Fatalf("%s가 두 페이지에 걸쳐 중복됩니다", id)
		}
		seen[id] = true
	}
	if len(seen) != 3 {
		t.Fatalf("페이징으로 %d건만 나왔습니다", len(seen))
	}
}

// TestIntegrationSellerControlsTheirOwnListing covers the seller side of a
// listing after it goes live: pausing it, resuming it, and the boundaries of
// who may do either.
func TestIntegrationSellerControlsTheirOwnListing(t *testing.T) {
	server, _ := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "controlseller", uniqueName("판매 제어 상품"), 70_000)
	browser := newClient(t, server.URL)
	browser.do(http.MethodGet, "/api/v1/talents/"+talentID, nil, http.StatusOK)

	// Pausing stops new orders while the work already in flight continues.
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("controlbuyer"))
	order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/status", map[string]any{"status": "paused"}, http.StatusOK)
	buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusNotFound)
	buyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+fmt.Sprint(order["id"])+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("pay")})

	// Resuming does not require another review, because the content is what was
	// already approved.
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/status", map[string]any{"status": "published"}, http.StatusOK)
	buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)

	// Nobody else can touch it, and nonsense transitions are refused.
	stranger := newClient(t, server.URL)
	stranger.register(uniqueName("controlstranger"))
	stranger.do(http.MethodPost, "/api/v1/talents/"+talentID+"/status", map[string]any{"status": "paused"}, http.StatusForbidden)
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/status", map[string]any{"status": "draft"}, http.StatusConflict)
}

// TestIntegrationListingViewsAreCountedHonestly covers the numbers a seller is
// meant to make pricing decisions from.
func TestIntegrationListingViewsAreCountedHonestly(t *testing.T) {
	server, _ := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "statsseller", uniqueName("성과 상품"), 80_000)
	stats := func() map[string]any {
		for _, row := range seller.do(http.MethodGet, "/api/v1/me/talents", nil, http.StatusOK)["items"].([]any) {
			item := row.(map[string]any)
			if fmt.Sprint(item["id"]) == talentID {
				return item
			}
		}
		t.Fatal("내 상품 목록에 상품이 없습니다")
		return nil
	}
	if stats()["views_30d"].(float64) != 0 {
		t.Fatalf("처음 조회수=%v", stats()["views_30d"])
	}

	// A seller looking at their own listing is not an audience.
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/view", nil, http.StatusNoContent)
	if stats()["views_30d"].(float64) != 0 {
		t.Fatal("판매자 본인의 조회가 집계됐습니다")
	}

	// A viewer counts once a day however many times they open the page.
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("statsbuyer"))
	for i := 0; i < 3; i++ {
		buyer.do(http.MethodPost, "/api/v1/talents/"+talentID+"/view", nil, http.StatusNoContent)
	}
	if got := stats()["views_30d"].(float64); got != 1 {
		t.Fatalf("같은 사람의 반복 조회가 %v로 집계됐습니다", got)
	}

	// A different viewer is a different view, and an order moves conversion.
	another := newClient(t, server.URL)
	another.register(uniqueName("statsbuyer2"))
	another.do(http.MethodPost, "/api/v1/talents/"+talentID+"/view", nil, http.StatusNoContent)
	buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
	current := stats()
	if current["views_30d"].(float64) != 2 || current["orders_30d"].(float64) != 1 {
		t.Fatalf("조회 %v · 주문 %v", current["views_30d"], current["orders_30d"])
	}
	if rate, ok := current["conversion_rate"].(float64); !ok || rate < 49 || rate > 51 {
		t.Fatalf("전환율=%v", current["conversion_rate"])
	}
}

// TestIntegrationOverdueOrderGivesTheBuyerAWayOut covers the case a paid buyer
// had no answer for: the seller went quiet and the money stayed in escrow.
func TestIntegrationOverdueOrderGivesTheBuyerAWayOut(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "overdueseller", uniqueName("납기 상품"), 90_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("overduebuyer"))
	order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
	orderID := fmt.Sprint(order["id"])
	buyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+orderID+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": uniqueName("pay")})
	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "IN_PROGRESS", "note": ""}, http.StatusOK)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "CANCEL_REQUESTED", "note": "연락이 없습니다"}, http.StatusOK)

	// While the deadline still stands, ending the order is an operator's call.
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "CANCELLED", "note": ""}, http.StatusForbidden)
	if detail := buyer.do(http.MethodGet, "/api/v1/orders/"+orderID, nil, http.StatusOK); detail["overdue"] != false {
		t.Fatalf("아직 납기 전인데 overdue=%v", detail["overdue"])
	}

	// Push the promised date well into the past.
	if _, err := pool.Exec(context.Background(), `UPDATE orders SET due_at=now()-interval '10 days' WHERE id=$1`, orderID); err != nil {
		t.Fatalf("납기 변경 실패: %v", err)
	}
	detail := buyer.do(http.MethodGet, "/api/v1/orders/"+orderID, nil, http.StatusOK)
	if detail["overdue"] != true || detail["overdue_days"].(float64) < 9 {
		t.Fatalf("overdue=%v days=%v", detail["overdue"], detail["overdue_days"])
	}

	// Now the buyer can end it themselves, and the escrow comes back.
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/transition", map[string]any{"to": "CANCELLED", "note": "납기 초과"}, http.StatusOK)
	balances, total := ledgerBalance(t, pool, orderID)
	if balances["Escrow"] != 0 {
		t.Fatalf("취소 후 에스크로 잔액=%d (전체 %v)", balances["Escrow"], balances)
	}
	if total != 0 {
		t.Fatalf("원장이 맞지 않습니다: %d", total)
	}
	// The seller cannot use the same door.
	if state := buyer.do(http.MethodGet, "/api/v1/orders/"+orderID, nil, http.StatusOK)["state"]; state != "CANCELLED" {
		t.Fatalf("주문 상태=%v", state)
	}
}

// TestIntegrationDisputeQueueLeadsWithTheLongestWait covers a work queue that
// showed the newest case first and so could starve the oldest one indefinitely.
func TestIntegrationDisputeQueueLeadsWithTheLongestWait(t *testing.T) {
	server, pool := integrationServer(t)
	operator := operatorClient(t, server, pool, "queueop")
	opened := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		seller, talentID, _ := sellTalent(t, server, fmt.Sprintf("queueseller%d", i), uniqueName("대기열 상품"), 50_000)
		buyer := newClient(t, server.URL)
		buyer.register(uniqueName("queuebuyer"))
		orderID := payAndDeliver(t, buyer, seller, talentID)
		dispute := buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": fmt.Sprintf("납품물이 요구사항과 다릅니다 %d", i), "evidence": []any{}}, http.StatusCreated)
		opened = append(opened, fmt.Sprint(dispute["id"]))
	}
	// Age the first one so it is unambiguously the longest wait.
	tag, err := pool.Exec(context.Background(), `UPDATE disputes SET created_at=now()-interval '10 days' WHERE id=$1`, opened[0])
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("분쟁 접수 시각 변경 실패: %v (%d행)", err, tag.RowsAffected())
	}

	page := operator.do(http.MethodGet, "/api/v1/admin/disputes?state=open", nil, http.StatusOK)
	rows := page["items"].([]any)
	if len(rows) < 3 {
		t.Fatalf("열린 분쟁 %d건", len(rows))
	}
	// Other tests share this database, so the check is on relative order among
	// this test's own three cases rather than on absolute position.
	position := map[string]int{}
	var aged map[string]any
	for index, row := range rows {
		item := row.(map[string]any)
		id := fmt.Sprint(item["id"])
		position[id] = index
		if id == opened[0] {
			aged = item
		}
	}
	if aged == nil {
		t.Fatal("오래된 분쟁이 목록에 없습니다")
	}
	if position[opened[0]] > position[opened[1]] || position[opened[0]] > position[opened[2]] {
		t.Fatalf("오래 기다린 분쟁이 뒤에 있습니다: %v", position)
	}
	// The wait is truncated to whole hours and the database clock need not agree
	// with this process to the second, so the assertion is on the order of
	// magnitude rather than on the exact boundary of ten days.
	if waiting, ok := aged["waiting_hours"].(float64); !ok || waiting < 200 {
		t.Fatalf("대기 시간=%v", aged["waiting_hours"])
	}
	// The queue reports the promise it is being measured against, and the
	// dashboard counts the cases that have broken it.
	if page["sla_hours"].(float64) <= 0 {
		t.Fatalf("SLA=%v", page["sla_hours"])
	}
	// The dashboard sits behind a different permission than the queue.
	auditorName := uniqueName("queueauditor")
	auditor := newClient(t, server.URL)
	auditor.register(auditorName)
	grantRole(t, pool, auditorName, "super_admin")
	auditor.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	auditor.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": auditorName, "password": "IntegrationPass!23"}, http.StatusOK)
	dashboard := auditor.do(http.MethodGet, "/api/v1/admin/dashboard", nil, http.StatusOK)
	if dashboard["breached_disputes"].(float64) < 1 {
		t.Fatalf("지연 분쟁 집계=%v", dashboard["breached_disputes"])
	}
}

// TestIntegrationApprovalQueueReportsTheWait covers the queue a seller's
// livelihood sits in, which was unbounded and measured nothing.
func TestIntegrationApprovalQueueReportsTheWait(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("approvalop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	policy := operator.do(http.MethodPost, "/api/v1/admin/approvals/policies", map[string]any{
		"resource_type": "talent_publish", "name": uniqueName("대기 검증 정책"), "enabled": true, "priority": 1,
		"conditions": map[string]any{"min_amount": 1_000}, "steps": []map[string]any{{"role": "operator", "min_approvals": 1}},
	}, http.StatusCreated)
	// A policy with decision history cannot be deleted, so it is switched off to
	// keep it from affecting later tests.
	t.Cleanup(func() {
		operator.do(http.MethodPut, "/api/v1/admin/approvals/policies/"+fmt.Sprint(policy["id"]), map[string]any{
			"resource_type": "talent_publish", "name": "대기 검증 종료", "enabled": false, "priority": 1,
			"conditions": map[string]any{"min_amount": 1_000}, "steps": []map[string]any{{"role": "operator", "min_approvals": 1}},
		}, http.StatusOK)
	})

	seller, talentID, _ := sellTalent(t, server, "approvalseller", uniqueName("승인 대기 상품"), 40_000)
	_ = talentID
	_ = seller

	page := operator.do(http.MethodGet, "/api/v1/admin/approvals/requests", nil, http.StatusOK)
	rows := page["items"].([]any)
	if len(rows) == 0 {
		t.Fatal("승인 대기열이 비어 있습니다")
	}
	if page["sla_hours"].(float64) <= 0 {
		t.Fatalf("승인 SLA=%v", page["sla_hours"])
	}
	target := fmt.Sprint(rows[0].(map[string]any)["id"])
	if _, ok := rows[0].(map[string]any)["waiting_hours"]; !ok {
		t.Fatal("대기 시간이 없습니다")
	}
	if _, err := pool.Exec(context.Background(), `UPDATE approval_requests SET created_at=now()-interval '5 days' WHERE id=$1`, target); err != nil {
		t.Fatalf("대기 시각 변경 실패: %v", err)
	}
	// The aged request now leads its queue and the dashboard counts it as late.
	aged := operator.do(http.MethodGet, "/api/v1/admin/approvals/requests", nil, http.StatusOK)["items"].([]any)
	if fmt.Sprint(aged[0].(map[string]any)["id"]) != target {
		t.Fatalf("가장 오래 기다린 요청이 맨 위가 아닙니다: %v", aged[0])
	}
	if hours := aged[0].(map[string]any)["waiting_hours"].(float64); hours < 100 {
		t.Fatalf("대기 시간=%v", hours)
	}
	if stale := operator.do(http.MethodGet, "/api/v1/admin/dashboard", nil, http.StatusOK)["stale_approvals"].(float64); stale < 1 {
		t.Fatalf("검토 지연 집계=%v", stale)
	}
}

// publishTalentOfType creates one more talent for a seller that already has a
// profile and returns the status publishing produced, which is what the
// approval policy decides.
func publishTalentOfType(t *testing.T, seller *client, serviceType, title string, price int64) (string, string) {
	t.Helper()
	talent := seller.do(http.MethodPost, "/api/v1/talents", map[string]any{
		"title": title, "summary": title, "description": title + " 상세 설명입니다.",
		"service_type": serviceType, "base_price": price, "delivery_days": 3, "currency": "KRW", "revision_count": 1,
		"scope_included": []string{}, "scope_excluded": []string{}, "deliverables": []string{}, "tags": []string{"test"},
		"faq": []any{}, "refund_policy": "전액 환불", "instant_order": true, "quote_required": false, "subscription_enabled": false,
		"packages":     []map[string]any{{"package_type": "BASIC", "name": "기본", "description": "기본", "price": price, "delivery_days": 3, "revision_count": 1, "features": []string{}, "deliverables": []string{}, "sort_order": 0, "active": true}},
		"requirements": []map[string]any{{"label": "요구사항", "help_text": "", "field_type": "textarea", "required": true, "options": []any{}, "validation": map[string]any{}, "sort_order": 0}},
	}, http.StatusCreated)
	talentID, _ := talent["id"].(string)
	if talentID == "" {
		t.Fatal("상품 식별자가 없습니다")
	}
	published := seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)
	return talentID, fmt.Sprint(published["status"])
}

// TestIntegrationApprovalPolicyArrayConditionsAreCheckedOnSave pins both halves
// of the same contract: saving refuses an array condition the matcher would
// skip, and a well formed one still decides publishing exactly as before. The
// matcher reads service_types with a []any assertion, so a policy stored with a
// bare string would quietly apply to every product instead of the ones it names.
func TestIntegrationApprovalPolicyArrayConditionsAreCheckedOnSave(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("arraycondop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	// Policies left enabled by earlier tests would also decide these products,
	// so they step aside while this one runs.
	var parked []string
	rows, err := pool.Query(context.Background(), `SELECT id FROM approval_policies WHERE resource_type='talent_publish' AND enabled`)
	if err != nil {
		t.Fatalf("기존 정책 조회 실패: %v", err)
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			parked = append(parked, id)
		}
	}
	rows.Close()
	if len(parked) > 0 {
		if _, err := pool.Exec(context.Background(), `UPDATE approval_policies SET enabled=false WHERE id=ANY($1)`, parked); err != nil {
			t.Fatalf("기존 정책 비활성화 실패: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `UPDATE approval_policies SET enabled=true WHERE id=ANY($1)`, parked)
		})
	}

	countPolicies := func() int {
		listed := operator.do(http.MethodGet, "/api/v1/admin/approvals/policies", nil, http.StatusOK)["items"].([]any)
		return len(listed)
	}
	before := countPolicies()

	for _, broken := range []struct {
		name  string
		key   string
		value any
	}{
		{"배열이 아닌 문자열", "service_types", "AI"},
		{"원소가 숫자", "service_types", []any{"AI", 3}},
		{"공백뿐인 문자열", "seller_levels", []any{"NEW", "  "}},
		{"배열이 아닌 문자열", "seller_levels", "NEW"},
	} {
		rejected := operator.do(http.MethodPost, "/api/v1/admin/approvals/policies", map[string]any{
			"resource_type": "talent_publish", "name": uniqueName("배열 조건 거부"), "enabled": true, "priority": 1,
			"conditions": map[string]any{broken.key: broken.value}, "steps": []map[string]any{{"role": "operator", "min_approvals": 1}},
		}, http.StatusBadRequest)
		failure, _ := rejected["error"].(map[string]any)
		if fmt.Sprint(failure["code"]) != "invalid_policy" {
			t.Fatalf("%s %s 거부 코드=%v", broken.key, broken.name, rejected["error"])
		}
		if message := fmt.Sprint(failure["message"]); !strings.Contains(message, broken.key) {
			t.Fatalf("%s %s 거부 메시지에 키 이름이 없습니다: %q", broken.key, broken.name, message)
		}
	}
	if after := countPolicies(); after != before {
		t.Fatalf("거부된 정책이 저장되었습니다: 이전 %d, 이후 %d", before, after)
	}

	// A well formed policy is stored and keeps deciding as it did before.
	policy := operator.do(http.MethodPost, "/api/v1/admin/approvals/policies", map[string]any{
		"resource_type": "talent_publish", "name": uniqueName("AI 검토 정책"), "enabled": true, "priority": 1,
		"conditions": map[string]any{"service_types": []string{"AI"}}, "steps": []map[string]any{{"role": "operator", "min_approvals": 1}},
	}, http.StatusCreated)
	policyID := fmt.Sprint(policy["id"])
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE approval_policies SET enabled=false WHERE id=$1`, policyID)
	})

	// Editing it with a broken array is refused too, and the stored row keeps
	// the array it had.
	updateRejected := operator.do(http.MethodPut, "/api/v1/admin/approvals/policies/"+policyID, map[string]any{
		"resource_type": "talent_publish", "name": "AI 검토 정책", "enabled": true, "priority": 1,
		"conditions": map[string]any{"service_types": "AI"}, "steps": []map[string]any{{"role": "operator", "min_approvals": 1}},
	}, http.StatusBadRequest)
	updateFailure, _ := updateRejected["error"].(map[string]any)
	if message := fmt.Sprint(updateFailure["message"]); !strings.Contains(message, "service_types") {
		t.Fatalf("수정 거부 메시지에 키 이름이 없습니다: %q", message)
	}
	var stored string
	if err := pool.QueryRow(context.Background(), `SELECT conditions->>'service_types' FROM approval_policies WHERE id=$1`, policyID).Scan(&stored); err != nil {
		t.Fatalf("저장된 조건 조회 실패: %v", err)
	}
	if stored != `["AI"]` {
		t.Fatalf("거부된 수정이 저장되었습니다: %s", stored)
	}

	// The same value now decides publishing on the matcher side: the AI product
	// waits for review, the human one goes straight out.
	seller, _, _ := sellTalent(t, server, "arraycondseller", uniqueName("사람 상품"), 40_000)
	aiID, aiStatus := publishTalentOfType(t, seller, "AI", uniqueName("AI 상품"), 40_000)
	if aiStatus != "review_pending" {
		t.Fatalf("service_types가 맞는 상품의 상태=%s", aiStatus)
	}
	humanID, humanStatus := publishTalentOfType(t, seller, "HUMAN", uniqueName("사람 상품"), 40_000)
	if humanStatus != "published" {
		t.Fatalf("service_types가 맞지 않는 상품의 상태=%s", humanStatus)
	}
	for id, want := range map[string]string{aiID: "review_pending", humanID: "published"} {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM talents WHERE id=$1`, id).Scan(&status); err != nil {
			t.Fatalf("상품 상태 조회 실패: %v", err)
		}
		if status != want {
			t.Fatalf("상품 %s의 저장된 상태=%s, 기대=%s", id, status, want)
		}
	}
}

// TestIntegrationBrokenPolicyConditionsCanStillBeDisabled covers the row that is
// already there. A policy saved before the condition check existed can hold an
// array the console now refuses, and the enable/disable switch sends the row
// back exactly as it was read. Refusing it there would leave an administrator no
// way to stop the policy, because deleting one that has handled a request is
// refused too. Changing the conditions to another broken value stays refused.
func TestIntegrationBrokenPolicyConditionsCanStillBeDisabled(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("brokencondop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	broken := map[string]any{"service_types": "AI", "seller_levels": []any{float64(1), nil}}
	var policyID string
	if err := pool.QueryRow(context.Background(), `INSERT INTO approval_policies(id,resource_type,name,enabled,priority,conditions,steps)
		VALUES(gen_random_uuid(),'talent_publish',$1,true,100,$2,'[{"role":"operator","min_approvals":1}]'::jsonb) RETURNING id`,
		uniqueName("예전에 저장된 정책"), broken).Scan(&policyID); err != nil {
		t.Fatalf("예전 정책 삽입 실패: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM approval_policies WHERE id=$1`, policyID) })

	storedConditions := func() map[string]any {
		var raw []byte
		if err := pool.QueryRow(context.Background(), `SELECT conditions FROM approval_policies WHERE id=$1`, policyID).Scan(&raw); err != nil {
			t.Fatalf("저장된 조건 조회 실패: %v", err)
		}
		var conditions map[string]any
		if err := json.Unmarshal(raw, &conditions); err != nil {
			t.Fatalf("저장된 조건 해석 실패: %v", err)
		}
		return conditions
	}

	// Read it back the way the console does, then send that row back with only
	// the switch moved.
	var listed map[string]any
	for _, raw := range operator.do(http.MethodGet, "/api/v1/admin/approvals/policies", nil, http.StatusOK)["items"].([]any) {
		if item, _ := raw.(map[string]any); item != nil && fmt.Sprint(item["id"]) == policyID {
			listed = item
		}
	}
	if listed == nil {
		t.Fatal("목록에 정책이 없습니다")
	}
	if !reflect.DeepEqual(listed["conditions"], broken) {
		t.Fatalf("목록이 조건을 그대로 돌려주지 않았습니다: %v", listed["conditions"])
	}
	toggle := map[string]any{"resource_type": listed["resource_type"], "name": listed["name"], "enabled": false, "priority": listed["priority"], "conditions": listed["conditions"], "steps": listed["steps"]}
	operator.do(http.MethodPut, "/api/v1/admin/approvals/policies/"+policyID, toggle, http.StatusOK)
	var enabled bool
	if err := pool.QueryRow(context.Background(), `SELECT enabled FROM approval_policies WHERE id=$1`, policyID).Scan(&enabled); err != nil {
		t.Fatalf("활성 상태 조회 실패: %v", err)
	}
	if enabled {
		t.Fatal("잘못된 조건을 가진 정책을 끄지 못했습니다")
	}
	if conditions := storedConditions(); !reflect.DeepEqual(conditions, broken) {
		t.Fatalf("전환이 조건을 바꿨습니다: %v", conditions)
	}

	// Putting a different broken value on the same row is still a 400, and the
	// row keeps what it had.
	changed := map[string]any{"resource_type": "talent_publish", "name": listed["name"], "enabled": false, "priority": listed["priority"],
		"conditions": map[string]any{"service_types": "HUMAN", "seller_levels": []any{float64(1), nil}}, "steps": listed["steps"]}
	rejected := operator.do(http.MethodPut, "/api/v1/admin/approvals/policies/"+policyID, changed, http.StatusBadRequest)
	failure, _ := rejected["error"].(map[string]any)
	if message := fmt.Sprint(failure["message"]); !strings.Contains(message, "service_types") {
		t.Fatalf("바뀐 잘못된 조건의 거부 메시지=%q", message)
	}
	if conditions := storedConditions(); !reflect.DeepEqual(conditions, broken) {
		t.Fatalf("거부된 수정이 저장되었습니다: %v", conditions)
	}
}

// TestIntegrationStaticAssetsCacheAndFailHonestly covers the two things route
// level code splitting made matter: a framework bundle that should never be
// revalidated, and a chunk that disappears when a deploy lands under an open
// tab.
func TestIntegrationStaticAssetsCacheAndFailHonestly(t *testing.T) {
	server, _ := integrationServer(t)
	client := newClient(t, server.URL)

	shell, err := client.raw(http.MethodGet, "/orders/does-not-exist-as-a-file")
	if err != nil {
		t.Fatalf("앱 셸 요청 실패: %v", err)
	}
	defer shell.Body.Close()
	if shell.StatusCode != http.StatusOK {
		t.Fatalf("앱 셸 상태=%d", shell.StatusCode)
	}
	if cache := shell.Header.Get("Cache-Control"); cache != "no-cache" {
		t.Fatalf("앱 셸 Cache-Control=%q", cache)
	}
	body, err := io.ReadAll(shell.Body)
	if err != nil {
		t.Fatalf("앱 셸 본문: %v", err)
	}
	// The shell paints the page background itself, so a slow bundle shows the
	// product rather than a white rectangle. A bundler change could drop this
	// silently.
	if !strings.Contains(string(body), "#root:empty") || !strings.Contains(string(body), `lang="ko"`) {
		t.Fatal("앱 셸이 첫 페인트용 스타일이나 언어 표기를 잃었습니다")
	}

	// A chunk name that no build ever produced must not come back as the shell.
	missing, err := client.raw(http.MethodGet, "/assets/AdminPage-deadbeef.js")
	if err != nil {
		t.Fatalf("없는 청크 요청 실패: %v", err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("없는 청크 상태=%d, HTML을 스크립트로 돌려주고 있습니다", missing.StatusCode)
	}

	// A real hashed asset is immutable.
	entries, err := fs.ReadDir(ui.Files, "dist/assets")
	if err != nil {
		t.Fatalf("자산 목록: %v", err)
	}
	var script string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			script = entry.Name()
			break
		}
	}
	if script == "" {
		t.Skip("빌드된 자산이 없습니다")
	}
	asset, err := client.raw(http.MethodGet, "/assets/"+script)
	if err != nil {
		t.Fatalf("자산 요청 실패: %v", err)
	}
	defer asset.Body.Close()
	if asset.StatusCode != http.StatusOK {
		t.Fatalf("자산 상태=%d", asset.StatusCode)
	}
	if cache := asset.Header.Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("해시된 자산 Cache-Control=%q", cache)
	}
}

// TestIntegrationReadinessFailsWhileDraining covers the window a rolling deploy
// lives in: the process has been told to stop but is still holding its socket.
func TestIntegrationReadinessFailsWhileDraining(t *testing.T) {
	server, _ := integrationServer(t)
	client := newClient(t, server.URL)
	client.do(http.MethodGet, "/health/ready", nil, http.StatusOK)

	// Whatever routes traffic here must be able to see the process leaving
	// before the socket closes under it.
	apiUnderTest.BeginDrain()
	body := client.do(http.MethodGet, "/health/ready", nil, http.StatusServiceUnavailable)
	if failure, ok := body["error"].(map[string]any); !ok || failure["code"] != "shutting_down" {
		t.Fatalf("종료 중 응답=%v", body)
	}
	// Liveness stays up: the process is alive and still finishing what it has.
	client.do(http.MethodGet, "/health/live", nil, http.StatusOK)
	// Requests already in flight are still served.
	client.do(http.MethodGet, "/api/v1/version", nil, http.StatusOK)
}

// TestIntegrationRequestLogCarriesTheOutcome covers a log that recorded every
// request identically, so a failure could not be found in it.
func TestIntegrationRequestLogCarriesTheOutcome(t *testing.T) {
	var captured bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&captured, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server, _ := integrationServer(t)
	apiUnderTest.Logger = logger
	client := newClient(t, server.URL)

	client.do(http.MethodGet, "/api/v1/version", nil, http.StatusOK)
	client.do(http.MethodGet, "/api/v1/me", nil, http.StatusUnauthorized)
	client.do(http.MethodGet, "/api/v1/talents/not-a-uuid", nil, http.StatusBadRequest)

	lines := strings.Split(strings.TrimSpace(captured.String()), "\n")
	seen := map[string]string{}
	for _, line := range lines {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil || entry["msg"] != "http request" {
			continue
		}
		path, _ := entry["path"].(string)
		status, ok := entry["status"].(float64)
		if !ok {
			t.Fatalf("상태 코드가 없는 로그: %s", line)
		}
		seen[path] = fmt.Sprintf("%s/%.0f", entry["level"], status)
	}
	if got := seen["/api/v1/version"]; got != "INFO/200" {
		t.Fatalf("성공 요청 로그=%q", got)
	}
	// A rejected request has to stand out from a served one, or nobody can find
	// it when someone reports a problem.
	if got := seen["/api/v1/me"]; got != "WARN/401" {
		t.Fatalf("인증 실패 로그=%q", got)
	}
	if got := seen["/api/v1/talents/not-a-uuid"]; got != "WARN/400" {
		t.Fatalf("잘못된 요청 로그=%q", got)
	}
}

// TestIntegrationSocketDeadlinesSurviveTheMiddleware guards the seam a logging
// wrapper broke once: if the response writer stops forwarding deadline control,
// every message socket silently dies at the server write timeout instead of
// staying open, and the symptom shows up minutes later far from the cause.
func TestIntegrationSocketDeadlinesSurviveTheMiddleware(t *testing.T) {
	var captured bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&captured, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server, _ := integrationServer(t)
	apiUnderTest.Logger = logger

	seller, talentID, _ := sellTalent(t, server, "socketseller", uniqueName("소켓 상품"), 30_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("socketbuyer"))
	order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
	_ = seller

	// The upgrade is refused without the right headers, which is fine: what
	// matters is that the handler ran and could reach the deadlines.
	response, err := buyer.raw(http.MethodGet, "/api/v1/orders/"+fmt.Sprint(order["id"])+"/messages/ws")
	if err != nil {
		t.Fatalf("소켓 요청 실패: %v", err)
	}
	defer response.Body.Close()

	if strings.Contains(captured.String(), "연결 타임아웃을 해제하지 못했습니다") {
		t.Fatal("미들웨어가 연결 데드라인 제어를 가로막고 있습니다")
	}
}

// TestIntegrationSellerCapacityHoldsUnderConcurrentOrders covers a check that
// ran before the transaction opened, so every simultaneous buyer passed it and
// a seller who said they can take one order at a time got all of them.
func TestIntegrationSellerCapacityHoldsUnderConcurrentOrders(t *testing.T) {
	server, _ := integrationServer(t)
	name := uniqueName("capacityseller")
	seller := newClient(t, server.URL)
	seller.register(name)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "한 번에 하나", "biography": "", "skills": []string{"go"},
		"capacity": 1, "settings": map[string]any{},
	}, http.StatusOK)
	talent := seller.do(http.MethodPost, "/api/v1/talents", talentPayload(uniqueName("동시성 상품"), "동시 주문 검증용 상품입니다.", 25_000), http.StatusCreated)
	talentID := fmt.Sprint(talent["id"])
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)

	const buyers = 6
	clients := make([]*client, buyers)
	for i := range clients {
		clients[i] = newClient(t, server.URL)
		clients[i].register(uniqueName("capacitybuyer"))
	}

	// All six press order at the same moment.
	var wg sync.WaitGroup
	codes := make([]int, buyers)
	start := make(chan struct{})
	for i := range clients {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			payload, _ := json.Marshal(map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "동시"}, "options": []any{}})
			request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/orders", bytes.NewReader(payload))
			if err != nil {
				return
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := clients[index].http.Do(request)
			if err != nil {
				return
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, response.Body)
			codes[index] = response.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()

	created := 0
	for _, code := range codes {
		if code == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("동시 주문 %d건이 생성됐습니다. 한도는 1건입니다: %v", created, codes)
	}
}

// TestIntegrationCapacityCheckSerialisesPerSeller tests the lock itself. The
// end to end concurrency test above depends on threads interleaving the way it
// hopes, and it passes even without the lock when the first order happens to
// commit first; this one does not depend on timing.
func TestIntegrationCapacityCheckSerialisesPerSeller(t *testing.T) {
	server, pool := integrationServer(t)
	name := uniqueName("lockseller")
	seller := newClient(t, server.URL)
	seller.register(name)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "잠금 검증", "biography": "", "skills": []string{"go"},
		"capacity": 1, "settings": map[string]any{},
	}, http.StatusOK)
	var sellerID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, name).Scan(&sellerID); err != nil {
		t.Fatalf("판매자 조회: %v", err)
	}

	first, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("첫 트랜잭션: %v", err)
	}
	defer first.Rollback(context.Background()) //nolint:errcheck
	if _, _, err := apiUnderTest.sellerAtCapacityTx(context.Background(), first, sellerID); err != nil {
		t.Fatalf("첫 확인: %v", err)
	}

	// The second transaction must wait for the first, otherwise both would be
	// deciding from the same view of how many orders are live.
	blocked := make(chan error, 1)
	go func() {
		second, err := pool.Begin(context.Background())
		if err != nil {
			blocked <- err
			return
		}
		defer second.Rollback(context.Background()) //nolint:errcheck
		_, _, err = apiUnderTest.sellerAtCapacityTx(context.Background(), second, sellerID)
		blocked <- err
	}()

	select {
	case err := <-blocked:
		t.Fatalf("두 번째 확인이 기다리지 않고 바로 끝났습니다: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := first.Rollback(context.Background()); err != nil {
		t.Fatalf("첫 트랜잭션 종료: %v", err)
	}
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatalf("두 번째 확인 실패: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("첫 트랜잭션이 끝났는데도 두 번째가 풀리지 않았습니다")
	}
}

// TestIntegrationIdempotencyKeysAreScopedToTheirOrder covers a uniqueness
// constraint that spanned every payment ever made: one buyer's ordinary retry
// key made the same string unusable for everyone else, forever.
func TestIntegrationIdempotencyKeysAreScopedToTheirOrder(t *testing.T) {
	server, _ := integrationServer(t)
	shared := "retry-1-" + uniqueName("k")

	pay := func(prefix string) (string, *client) {
		seller, talentID, _ := sellTalent(t, server, prefix+"seller", uniqueName("멱등 상품"), 15_000)
		_ = seller
		buyer := newClient(t, server.URL)
		buyer.register(uniqueName(prefix + "buyer"))
		order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
		return fmt.Sprint(order["id"]), buyer
	}

	firstOrder, firstBuyer := pay("idem1")
	firstBuyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+firstOrder+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": shared})

	// A different buyer, a different order, the same everyday key.
	secondOrder, secondBuyer := pay("idem2")
	secondBuyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+secondOrder+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": shared})

	// Within one order the key still means "this is the same request".
	replay := firstBuyer.doWithHeaders(http.MethodPost, "/api/v1/orders/"+firstOrder+"/pay", map[string]any{}, http.StatusOK, map[string]string{"Idempotency-Key": shared})
	if replay["idempotent_replay"] != true {
		t.Fatalf("같은 주문의 재시도가 새 결제로 처리됐습니다: %v", replay)
	}
}

// TestIntegrationSellersCanAnswerAReview covers the right of reply: a review
// used to be the last word on a page the seller depends on.
func TestIntegrationSellersCanAnswerAReview(t *testing.T) {
	server, _ := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "replyseller", uniqueName("답글 상품"), 45_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("replybuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)
	review := buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/review", map[string]any{
		"quality": 2, "communication": 3, "timeliness": 2, "professionalism": 3,
		"repurchase": false, "body": "납기가 하루 늦었습니다.",
	}, http.StatusCreated)
	reviewID := fmt.Sprint(review["id"])

	// Only the seller who did the work may answer it.
	stranger := newClient(t, server.URL)
	stranger.register(uniqueName("replystranger"))
	stranger.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "무관", "biography": "", "skills": []string{"go"}, "capacity": 5, "settings": map[string]any{},
	}, http.StatusOK)
	stranger.do(http.MethodPost, "/api/v1/reviews/"+reviewID+"/reply", map[string]any{"body": "제가 답합니다"}, http.StatusForbidden)
	buyer.do(http.MethodPost, "/api/v1/reviews/"+reviewID+"/reply", map[string]any{"body": "제 리뷰에 제가 답합니다"}, http.StatusForbidden)

	seller.do(http.MethodPost, "/api/v1/reviews/"+reviewID+"/reply", map[string]any{"body": "일정 관리가 부족했습니다. 다음에는 중간 공유를 더 자주 드리겠습니다."}, http.StatusOK)

	// The reply is published under the review, and the review itself is
	// untouched: the seller answers, they do not edit.
	anonymous := newClient(t, server.URL)
	page := anonymous.do(http.MethodGet, "/api/v1/talents/"+talentID+"/reviews", nil, http.StatusOK)
	first := page["items"].([]any)[0].(map[string]any)
	if first["body"] != "납기가 하루 늦었습니다." {
		t.Fatalf("리뷰 본문이 바뀌었습니다: %v", first["body"])
	}
	if reply, _ := first["seller_reply"].(string); !strings.Contains(reply, "중간 공유") {
		t.Fatalf("판매자 답글=%v", first["seller_reply"])
	}
	if first["seller_replied_at"] == nil {
		t.Fatal("답글 시각이 없습니다")
	}
	if first["average"].(float64) != 2.5 {
		t.Fatalf("답글이 점수를 바꿨습니다: %v", first["average"])
	}

	// The seller can take their own reply back.
	seller.do(http.MethodDelete, "/api/v1/reviews/"+reviewID+"/reply", nil, http.StatusNoContent)
	after := anonymous.do(http.MethodGet, "/api/v1/talents/"+talentID+"/reviews", nil, http.StatusOK)["items"].([]any)[0].(map[string]any)
	if after["seller_reply"] != nil {
		t.Fatalf("삭제 후 답글=%v", after["seller_reply"])
	}
}

// TestIntegrationSellersFindTheReviewsAwaitingAnAnswer covers the other half of
// the right of reply: being able to answer is worth little if finding what
// needs answering means opening every listing in turn.
func TestIntegrationSellersFindTheReviewsAwaitingAnAnswer(t *testing.T) {
	server, _ := integrationServer(t)
	name := uniqueName("inboxseller")
	seller := newClient(t, server.URL)
	seller.register(name)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "후기함", "biography": "", "skills": []string{"go"}, "capacity": 10, "settings": map[string]any{},
	}, http.StatusOK)

	reviewIDs := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		talent := seller.do(http.MethodPost, "/api/v1/talents", talentPayload(uniqueName("후기함 상품"), "후기함 검증용 상품입니다.", 30_000), http.StatusCreated)
		talentID := fmt.Sprint(talent["id"])
		seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)
		buyer := newClient(t, server.URL)
		buyer.register(uniqueName("inboxbuyer"))
		orderID := payAndDeliver(t, buyer, seller, talentID)
		buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)
		review := buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/review", map[string]any{
			"quality": 4, "communication": 4, "timeliness": 4, "professionalism": 4,
			"repurchase": true, "body": fmt.Sprintf("후기 %d번입니다.", i),
		}, http.StatusCreated)
		reviewIDs = append(reviewIDs, fmt.Sprint(review["id"]))
	}

	inbox := seller.do(http.MethodGet, "/api/v1/me/reviews", nil, http.StatusOK)
	if len(inbox["items"].([]any)) != 2 || inbox["unanswered"].(float64) != 2 {
		t.Fatalf("후기함=%v건, 미답변=%v", len(inbox["items"].([]any)), inbox["unanswered"])
	}
	first := inbox["items"].([]any)[0].(map[string]any)
	// Which listing a review belongs to is the context a seller needs to answer.
	if first["talent_title"] == nil || first["order_number"] == nil {
		t.Fatalf("후기에 상품·주문 정보가 없습니다: %v", first)
	}

	seller.do(http.MethodPost, "/api/v1/reviews/"+reviewIDs[0]+"/reply", map[string]any{"body": "이용해 주셔서 감사합니다."}, http.StatusOK)
	pending := seller.do(http.MethodGet, "/api/v1/me/reviews?unanswered=1", nil, http.StatusOK)
	rows := pending["items"].([]any)
	if len(rows) != 1 || pending["unanswered"].(float64) != 1 {
		t.Fatalf("답글 후 미답변=%d건 (%v)", len(rows), pending["unanswered"])
	}
	if fmt.Sprint(rows[0].(map[string]any)["id"]) != reviewIDs[1] {
		t.Fatalf("남은 미답변 후기가 다릅니다: %v", rows[0])
	}

	// The list belongs to the seller it is about.
	other := newClient(t, server.URL)
	other.register(uniqueName("inboxother"))
	other.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "타인", "biography": "", "skills": []string{"go"}, "capacity": 5, "settings": map[string]any{},
	}, http.StatusOK)
	if rows := other.do(http.MethodGet, "/api/v1/me/reviews", nil, http.StatusOK)["items"].([]any); len(rows) != 0 {
		t.Fatalf("남의 후기 %d건이 보입니다", len(rows))
	}
}

// TestIntegrationOrderFormIsEnforced covers a seller's order form that only
// ever existed in the buyer's browser: the server took whatever arrived, so a
// required field could be skipped and the seller started work with no brief.
func TestIntegrationOrderFormIsEnforced(t *testing.T) {
	server, _ := integrationServer(t)
	name := uniqueName("formseller")
	seller := newClient(t, server.URL)
	seller.register(name)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "양식", "biography": "", "skills": []string{"go"}, "capacity": 10, "settings": map[string]any{},
	}, http.StatusOK)
	payload := talentPayload(uniqueName("양식 상품"), "주문 양식 검증용 상품입니다.", 20_000)
	payload["requirements"] = []map[string]any{
		{"label": "목표", "help_text": "", "field_type": "textarea", "required": true, "options": []any{}, "validation": map[string]any{}, "sort_order": 0},
		{"label": "참고 링크", "help_text": "", "field_type": "text", "required": false, "options": []any{}, "validation": map[string]any{}, "sort_order": 1},
	}
	talent := seller.do(http.MethodPost, "/api/v1/talents", payload, http.StatusCreated)
	talentID := fmt.Sprint(talent["id"])
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)

	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("formbuyer"))
	order := func(requirements map[string]any, want int) map[string]any {
		return buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": requirements, "options": []any{}}, want)
	}

	// An empty brief, and a blank one, are both refused by name.
	refused := order(map[string]any{}, http.StatusBadRequest)
	if failure, _ := refused["error"].(map[string]any); failure == nil || !strings.Contains(fmt.Sprint(failure["message"]), "목표") {
		t.Fatalf("거절 사유가 항목을 짚어 주지 않습니다: %v", refused)
	}
	order(map[string]any{"목표": "   "}, http.StatusBadRequest)

	// The answers are stored under the seller's own labels, whichever key the
	// client used, and unknown keys are dropped rather than kept.
	detail := order(map[string]any{"목표": "로고를 새로 만들고 싶습니다.", "존재하지 않는 항목": "무시"}, http.StatusCreated)
	workspace := buyer.do(http.MethodGet, "/api/v1/orders/"+fmt.Sprint(detail["id"]), nil, http.StatusOK)
	stored := workspace["requirements"].(map[string]any)
	if stored["목표"] != "로고를 새로 만들고 싶습니다." {
		t.Fatalf("저장된 요구사항=%v", stored)
	}
	if _, present := stored["존재하지 않는 항목"]; present {
		t.Fatalf("양식에 없는 항목이 저장됐습니다: %v", stored)
	}
	if _, present := stored["참고 링크"]; present {
		t.Fatalf("비워 둔 선택 항목이 저장됐습니다: %v", stored)
	}

	// A brief nobody could read is refused rather than written to the order.
	order(map[string]any{"목표": strings.Repeat("가", 6000)}, http.StatusBadRequest)
}

// TestIntegrationQuoteOrderCarriesTheRequestAsItsBrief covers why the listing's
// own form is not demanded a second time on a quote: the buyer already wrote
// the brief, and it has to reach the order.
func TestIntegrationQuoteOrderCarriesTheRequestAsItsBrief(t *testing.T) {
	server, _ := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "briefseller", uniqueName("견적 브리프 상품"), 200_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("briefbuyer"))
	description := "관리자 페이지가 포함된 회사 홈페이지를 만들고 싶습니다. 예산은 300만원입니다."
	rfq := buyer.do(http.MethodPost, "/api/v1/rfqs", map[string]any{
		"title": uniqueName("홈페이지 제작"), "description": description, "requirements": map[string]any{},
		"budget_min": 1_000_000, "budget_max": 3_000_000, "currency": "KRW",
	}, http.StatusCreated)
	quote := seller.do(http.MethodPost, "/api/v1/quotes", map[string]any{
		"rfq_id": fmt.Sprint(rfq["id"]), "amount": 2_500_000, "currency": "KRW", "delivery_days": 20,
		"talent_id": talentID, "scope": map[string]any{"description": "제안드립니다."}, "milestones": []any{},
	}, http.StatusCreated)

	// The listing's required field is not demanded again, and the request the
	// seller priced against travels to the order.
	accepted := buyer.do(http.MethodPost, "/api/v1/quotes/"+fmt.Sprint(quote["id"])+"/accept", map[string]any{"requirements": map[string]any{}}, http.StatusCreated)
	workspace := buyer.do(http.MethodGet, "/api/v1/orders/"+fmt.Sprint(accepted["order_id"]), nil, http.StatusOK)
	stored := workspace["requirements"].(map[string]any)
	if stored["견적 요청 내용"] != description {
		t.Fatalf("주문에 견적 요청 내용이 실리지 않았습니다: %v", stored)
	}
}

// TestIntegrationBuyersCanAskBeforeOrdering covers the conversation that
// decides whether there will be an order at all, which previously had nowhere
// to happen: messages existed only inside an order.
func TestIntegrationBuyersCanAskBeforeOrdering(t *testing.T) {
	server, _ := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "askseller", uniqueName("문의 상품"), 120_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("askbuyer"))

	thread := buyer.do(http.MethodPost, "/api/v1/talents/"+talentID+"/inquiries", map[string]any{"body": "패키지에 원본 파일도 포함되나요?"}, http.StatusCreated)
	inquiryID := fmt.Sprint(thread["id"])

	// Asking again continues the same conversation rather than opening a second
	// one the seller has to notice separately.
	buyer.do(http.MethodPost, "/api/v1/talents/"+talentID+"/inquiries", map[string]any{"body": "그리고 수정은 몇 번까지인가요?"}, http.StatusCreated)
	inbox := buyer.do(http.MethodGet, "/api/v1/me/inquiries", nil, http.StatusOK)["items"].([]any)
	if len(inbox) != 1 {
		t.Fatalf("문의 스레드 %d개", len(inbox))
	}
	if inbox[0].(map[string]any)["role"] != "buyer" {
		t.Fatalf("구매자 역할=%v", inbox[0].(map[string]any)["role"])
	}

	// The seller sees it on their side of the same list, with what is waiting.
	sellerInbox := seller.do(http.MethodGet, "/api/v1/me/inquiries", nil, http.StatusOK)["items"].([]any)
	if len(sellerInbox) != 1 {
		t.Fatalf("판매자 문의 %d개", len(sellerInbox))
	}
	row := sellerInbox[0].(map[string]any)
	if row["role"] != "seller" || row["unread"].(float64) != 2 {
		t.Fatalf("판매자가 본 문의=%v", row)
	}

	// Reading the thread is what marks it read, and both sides see the whole
	// conversation.
	messages := seller.do(http.MethodGet, "/api/v1/inquiries/"+inquiryID+"/messages", nil, http.StatusOK)
	if len(messages["items"].([]any)) != 2 || messages["talent_title"] == nil {
		t.Fatalf("문의 메시지=%v", messages)
	}
	if after := seller.do(http.MethodGet, "/api/v1/me/inquiries", nil, http.StatusOK)["items"].([]any); after[0].(map[string]any)["unread"].(float64) != 0 {
		t.Fatalf("읽은 뒤에도 안 읽음이 남아 있습니다: %v", after[0])
	}
	seller.do(http.MethodPost, "/api/v1/inquiries/"+inquiryID+"/messages", map[string]any{"body": "원본 파일 포함이고 수정은 2회입니다."}, http.StatusCreated)
	if unread := buyer.do(http.MethodGet, "/api/v1/me/inquiries", nil, http.StatusOK)["items"].([]any)[0].(map[string]any)["unread"].(float64); unread != 1 {
		t.Fatalf("구매자 안 읽음=%v", unread)
	}

	// A seller who is not told about a question will not answer it, so the
	// notification is the part that makes this feature exist.
	eventually(t, "판매자에게 문의 알림", 20*time.Second, func() bool {
		for _, row := range seller.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
			item := row.(map[string]any)
			if item["event_type"] == "InquiryMessageCreated" && strings.Contains(fmt.Sprint(item["body"]), "문의") {
				return true
			}
		}
		return false
	})
	// The buyer hears about the seller's answer but never about their own
	// question, so the check is on who wrote the message rather than on how
	// many notifications happen to be in the inbox by now.
	buyerID := fmt.Sprint(buyer.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)["id"])
	for _, row := range buyer.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
		item := row.(map[string]any)
		if item["event_type"] != "InquiryMessageCreated" {
			continue
		}
		if data, _ := item["data"].(map[string]any); data != nil && fmt.Sprint(data["actor"]) == buyerID {
			t.Fatalf("자기가 보낸 문의로 알림을 받았습니다: %v", item)
		}
	}

	// It is a private conversation, and a seller cannot interview themselves.
	stranger := newClient(t, server.URL)
	stranger.register(uniqueName("askstranger"))
	stranger.do(http.MethodGet, "/api/v1/inquiries/"+inquiryID+"/messages", nil, http.StatusForbidden)
	stranger.do(http.MethodPost, "/api/v1/inquiries/"+inquiryID+"/messages", map[string]any{"body": "끼어들기"}, http.StatusForbidden)
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/inquiries", map[string]any{"body": "제 상품에 제가 문의"}, http.StatusBadRequest)
	buyer.do(http.MethodPost, "/api/v1/talents/"+talentID+"/inquiries", map[string]any{"body": "짧"}, http.StatusBadRequest)
}

// TestIntegrationAgentCanResearchBeforeBuying covers the half of the agent
// surface that decides whether to buy at all. An agent could search and order
// but could not read a review or ask a question, so it was choosing on a rating
// number alone.
func TestIntegrationAgentCanResearchBeforeBuying(t *testing.T) {
	server, pool := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "agentresearchseller", uniqueName("에이전트 조사 상품"), 60_000)
	reviewer := newClient(t, server.URL)
	reviewer.register(uniqueName("agentreviewer"))
	orderID := payAndDeliver(t, reviewer, seller, talentID)
	reviewer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)
	reviewer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/review", map[string]any{
		"quality": 5, "communication": 5, "timeliness": 4, "professionalism": 5,
		"repurchase": true, "body": "원본 파일까지 챙겨주셨습니다.",
	}, http.StatusCreated)

	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("researchagent"))
	created := buyer.do(http.MethodPost, "/api/v1/me/api-keys", map[string]any{
		"name": "조사 에이전트 키", "scopes": []string{"mcp.use", "orders.buy"}, "allowed_cidrs": []string{}, "rate_limit_per_minute": 600,
	}, http.StatusCreated)
	agent := &mcpClient{t: t, base: server.URL, key: fmt.Sprint(created["secret"]), http: &http.Client{Timeout: 20 * time.Second}}
	_ = pool

	if categories := fmt.Sprint(agent.tool("list_categories", map[string]any{})); !strings.Contains(categories, "디자인") {
		t.Fatalf("카테고리 도구 응답=%s", truncate(categories))
	}
	reviews := fmt.Sprint(agent.tool("list_reviews", map[string]any{"talent_id": talentID}))
	if !strings.Contains(reviews, "원본 파일까지") {
		t.Fatalf("후기 도구가 본문을 돌려주지 않습니다: %s", truncate(reviews))
	}
	// Budget is the constraint an agent is usually given, so it has to reach the
	// search rather than being applied by the agent afterwards.
	cheap := fmt.Sprint(agent.tool("search_talents", map[string]any{"query": "", "price_max": 1000, "limit": 50}))
	if strings.Contains(cheap, talentID) {
		t.Fatalf("예산 상한을 넘는 상품이 검색됐습니다: %s", truncate(cheap))
	}
	within := fmt.Sprint(agent.tool("search_talents", map[string]any{"query": "", "price_max": 100000, "limit": 50}))
	if !strings.Contains(within, talentID) {
		t.Fatalf("예산 안의 상품이 검색되지 않습니다: %s", truncate(within))
	}

	// And it can ask the question a person would ask before ordering.
	agent.tool("ask_seller", map[string]any{"talent_id": talentID, "body": "원본 파일도 함께 주시나요?"})
	threads := fmt.Sprint(agent.tool("list_inquiries", map[string]any{}))
	if !strings.Contains(threads, "에이전트 조사 상품") {
		t.Fatalf("문의 목록=%s", truncate(threads))
	}
}

func truncate(value string) string {
	if len([]rune(value)) > 400 {
		return string([]rune(value)[:400]) + "…"
	}
	return value
}

// TestIntegrationSellerProfileIsPublicAndEarned covers the page a buyer reads
// before paying a stranger. Every figure on it has to come from completed
// transactions rather than from what the seller says about themselves.
func TestIntegrationSellerProfileIsPublicAndEarned(t *testing.T) {
	server, pool := integrationServer(t)
	name := uniqueName("profileseller")
	seller := newClient(t, server.URL)
	seller.register(name)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "브랜드 아이덴티티 전문", "biography": "10년째 로고를 만듭니다.",
		"skills": []string{"branding", "logo"}, "capacity": 10, "settings": map[string]any{},
	}, http.StatusOK)
	var sellerID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, name).Scan(&sellerID); err != nil {
		t.Fatalf("판매자 조회: %v", err)
	}
	anonymous := newClient(t, server.URL)

	// Someone with nothing published is not a seller to the public, and their
	// account is not something the endpoint confirms exists.
	anonymous.do(http.MethodGet, "/api/v1/sellers/"+sellerID.String(), nil, http.StatusNotFound)

	talent := seller.do(http.MethodPost, "/api/v1/talents", talentPayload(uniqueName("프로필 상품"), "판매자 프로필 검증용 상품입니다.", 90_000), http.StatusCreated)
	talentID := fmt.Sprint(talent["id"])
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)

	profile := anonymous.do(http.MethodGet, "/api/v1/sellers/"+sellerID.String(), nil, http.StatusOK)
	if profile["headline"] != "브랜드 아이덴티티 전문" || profile["biography"] != "10년째 로고를 만듭니다." {
		t.Fatalf("판매자 소개=%v", profile)
	}
	if profile["published_talents"].(float64) != 1 || profile["completed_orders"].(float64) != 0 {
		t.Fatalf("판매자 지표=%v", profile)
	}
	// The private side of the profile stays private.
	for _, hidden := range []string{"email", "username", "capacity"} {
		if _, present := profile[hidden]; present {
			t.Fatalf("공개 프로필에 %s가 들어 있습니다: %v", hidden, profile)
		}
	}
	if listed := anonymous.do(http.MethodGet, "/api/v1/sellers/"+sellerID.String()+"/talents", nil, http.StatusOK)["items"].([]any); len(listed) != 1 {
		t.Fatalf("판매자 상품 %d개", len(listed))
	}

	// A completed order is what moves the numbers.
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("profilebuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/review", map[string]any{
		"quality": 5, "communication": 5, "timeliness": 5, "professionalism": 5, "repurchase": true, "body": "정확했습니다.",
	}, http.StatusCreated)
	after := anonymous.do(http.MethodGet, "/api/v1/sellers/"+sellerID.String(), nil, http.StatusOK)
	if after["completed_orders"].(float64) != 1 {
		t.Fatalf("완료 건수=%v", after["completed_orders"])
	}
	if after["rating_count"].(float64) != 1 {
		t.Fatalf("후기 건수=%v", after["rating_count"])
	}

	// The numbers on the profile need the sentences behind them, gathered across
	// every listing rather than hidden inside each one.
	sellerReviews := anonymous.do(http.MethodGet, "/api/v1/sellers/"+sellerID.String()+"/reviews", nil, http.StatusOK)["items"].([]any)
	if len(sellerReviews) != 1 {
		t.Fatalf("판매자 후기 %d건", len(sellerReviews))
	}
	entry := sellerReviews[0].(map[string]any)
	if entry["body"] != "정확했습니다." || entry["talent_title"] == nil {
		t.Fatalf("판매자 후기=%v", entry)
	}
	if masked, _ := entry["buyer_name"].(string); !strings.Contains(masked, "*") {
		t.Fatalf("구매자 이름이 가려지지 않았습니다: %q", masked)
	}

	// A suspended seller disappears from the public surface.
	if _, err := pool.Exec(context.Background(), `UPDATE users SET status='suspended' WHERE id=$1`, sellerID); err != nil {
		t.Fatalf("정지 처리: %v", err)
	}
	anonymous.do(http.MethodGet, "/api/v1/sellers/"+sellerID.String(), nil, http.StatusNotFound)
	anonymous.do(http.MethodGet, "/api/v1/sellers/"+sellerID.String()+"/reviews", nil, http.StatusNotFound)
}

// TestIntegrationBudgetExhaustionReachesTheOwner covers an event the product
// advertised as subscribable and had never emitted: a company budget stops
// working, a member is refused, and the person who can top it up hears nothing.
func TestIntegrationBudgetExhaustionReachesTheOwner(t *testing.T) {
	server, _ := integrationServer(t)
	owner := newClient(t, server.URL)
	owner.register(uniqueName("budgetowner"))
	memberName := uniqueName("budgetmember")
	member := newClient(t, server.URL)
	member.register(memberName)
	organization := owner.do(http.MethodPost, "/api/v1/organizations", map[string]any{"name": uniqueName("예산 알림사")}, http.StatusCreated)
	orgID := fmt.Sprint(organization["id"])
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/members", map[string]any{"account": memberName, "role": "member"}, http.StatusCreated)
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/budgets", map[string]any{"name": "소진 검증 예산", "amount": 50_000}, http.StatusCreated)

	_, talentID, _ := sellTalent(t, server, "budgetseller", uniqueName("예산 상품"), 80_000)
	order := map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}, "organization_id": orgID}
	member.do(http.MethodPost, "/api/v1/orders", order, http.StatusConflict)

	eventually(t, "예산 소진 알림", 20*time.Second, func() bool {
		for _, row := range owner.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
			if row.(map[string]any)["event_type"] == "BudgetExhausted" {
				return true
			}
		}
		return false
	})

	// A plain member is not who tops up a budget, so they are not told about it.
	for _, row := range member.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
		if row.(map[string]any)["event_type"] == "BudgetExhausted" {
			t.Fatal("예산 관리 권한이 없는 구성원에게 알림이 갔습니다")
		}
	}

	// A second refusal does not announce the same exhaustion again.
	member.do(http.MethodPost, "/api/v1/orders", order, http.StatusConflict)
	count := func() int {
		total := 0
		for _, row := range owner.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
			if row.(map[string]any)["event_type"] == "BudgetExhausted" {
				total++
			}
		}
		return total
	}
	time.Sleep(2 * time.Second)
	if repeated := count(); repeated != 1 {
		t.Fatalf("같은 소진으로 알림이 %d번 갔습니다", repeated)
	}
}

// TestIntegrationDisablingEnterpriseStopsCompanySpending covers a switch that
// used to hide the section from the menu while leaving the API wide open: an
// operator who turned the feature off still had members spending company
// budget through it.
func TestIntegrationDisablingEnterpriseStopsCompanySpending(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("featureop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	owner := newClient(t, server.URL)
	owner.register(uniqueName("featureowner"))
	organization := owner.do(http.MethodPost, "/api/v1/organizations", map[string]any{"name": uniqueName("플래그 검증사")}, http.StatusCreated)
	orgID := fmt.Sprint(organization["id"])
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/budgets", map[string]any{"name": "검증 예산", "amount": 500_000}, http.StatusCreated)
	_, talentID, _ := sellTalent(t, server, "featureseller", uniqueName("플래그 상품"), 50_000)
	companyOrder := map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}, "organization_id": orgID}
	first := owner.do(http.MethodPost, "/api/v1/orders", companyOrder, http.StatusCreated)
	if first["budget_reserved"].(float64) != 50_000 {
		t.Fatalf("예약 금액=%v", first["budget_reserved"])
	}

	operator.do(http.MethodPut, "/api/v1/admin/feature-flags/enterprise", map[string]any{"enabled": false}, http.StatusOK)
	t.Cleanup(func() {
		operator.do(http.MethodPut, "/api/v1/admin/feature-flags/enterprise", map[string]any{"enabled": true}, http.StatusOK)
	})
	// The cache in front of the flags is short lived by design.
	eventually(t, "기능 플래그 반영", 20*time.Second, func() bool {
		_, err := owner.raw(http.MethodGet, "/api/v1/me/organizations")
		if err != nil {
			return false
		}
		response, _ := owner.raw(http.MethodGet, "/api/v1/me/organizations")
		defer response.Body.Close()
		return response.StatusCode == http.StatusNotFound
	})

	// Every part of the feature is closed, not just the part with a menu entry.
	owner.do(http.MethodGet, "/api/v1/organizations/"+orgID, nil, http.StatusNotFound)
	owner.do(http.MethodGet, "/api/v1/organizations/"+orgID+"/orders", nil, http.StatusNotFound)
	owner.do(http.MethodPost, "/api/v1/organizations/"+orgID+"/budgets", map[string]any{"name": "몰래 추가", "amount": 1_000_000}, http.StatusNotFound)
	// And the spending itself stops, which is the part that costs money.
	owner.do(http.MethodPost, "/api/v1/orders", companyOrder, http.StatusNotFound)

	// A personal order is unaffected: the switch governs company accounts, not
	// the marketplace.
	owner.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "개인"}, "options": []any{}}, http.StatusCreated)
}

// TestIntegrationOperatorsCanRevokeALeakedKey covers a permission that existed,
// was granted to the security admin role and appeared on the roles screen while
// no code had ever read it.
func TestIntegrationOperatorsCanRevokeALeakedKey(t *testing.T) {
	server, pool := integrationServer(t)
	ownerName := uniqueName("keyowner")
	owner := newClient(t, server.URL)
	owner.register(ownerName)
	created := owner.do(http.MethodPost, "/api/v1/me/api-keys", map[string]any{
		"name": "유출된 키", "scopes": []string{"mcp.use"}, "allowed_cidrs": []string{}, "rate_limit_per_minute": 60,
	}, http.StatusCreated)
	secret := fmt.Sprint(created["secret"])
	var ownerID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, ownerName).Scan(&ownerID); err != nil {
		t.Fatalf("계정 조회: %v", err)
	}
	agent := &mcpClient{t: t, base: server.URL, key: secret, http: &http.Client{Timeout: 20 * time.Second}}
	if response := agent.call("tools/list", map[string]any{}); response["result"] == nil {
		t.Fatalf("키가 처음부터 동작하지 않습니다: %v", response)
	}

	// An ordinary account cannot reach someone else's keys.
	stranger := newClient(t, server.URL)
	stranger.register(uniqueName("keystranger"))
	stranger.do(http.MethodGet, "/api/v1/admin/users/"+ownerID.String()+"/api-keys", nil, http.StatusForbidden)

	securityName := uniqueName("keysecurity")
	security := newClient(t, server.URL)
	security.register(securityName)
	grantRole(t, pool, securityName, "security_admin")
	security.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	security.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": securityName, "password": "IntegrationPass!23"}, http.StatusOK)

	keys := security.do(http.MethodGet, "/api/v1/admin/users/"+ownerID.String()+"/api-keys", nil, http.StatusOK)["items"].([]any)
	if len(keys) != 1 {
		t.Fatalf("키 %d개", len(keys))
	}
	entry := keys[0].(map[string]any)
	// The listing must not hand an administrator something they could use as
	// the key's owner.
	if _, leaked := entry["secret"]; leaked {
		t.Fatalf("관리 목록에 키 원문이 들어 있습니다: %v", entry)
	}

	security.do(http.MethodDelete, "/api/v1/admin/api-keys/"+fmt.Sprint(entry["id"]), nil, http.StatusNoContent)
	// A revoked key stops authenticating, so the check has to look at the HTTP
	// status rather than at a JSON-RPC body that never arrives.
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/me", nil)
	if err != nil {
		t.Fatalf("요청 생성: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("폐기 확인 요청: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("폐기된 키가 아직 동작합니다: status=%d", response.StatusCode)
	}
	// Revoking twice is not an error the second time round, it is a 404.
	security.do(http.MethodDelete, "/api/v1/admin/api-keys/"+fmt.Sprint(entry["id"]), nil, http.StatusNotFound)
}

// TestIntegrationExpiredSessionIsRefusedClearly covers what the browser relies
// on to notice that a session ended. A session that expires while someone is
// working used to leave the application looking signed in: the header kept the
// account, every menu stayed, and each action failed with its own red toast
// telling them to sign in without offering a way to. The client now changes
// state on a single 401, which only works if the server answers one.
func TestIntegrationExpiredSessionIsRefusedClearly(t *testing.T) {
	server, pool := integrationServer(t)
	name := uniqueName("expiryuser")
	user := newClient(t, server.URL)
	user.register(name)
	user.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)

	// Age the session past its expiry the way twelve hours would.
	if _, err := pool.Exec(context.Background(), `UPDATE sessions SET expires_at=now()-interval '1 minute'
		WHERE user_id=(SELECT id FROM users WHERE username=$1)`, name); err != nil {
		t.Fatalf("세션 만료 처리: %v", err)
	}

	// Every authenticated surface answers the same way, so one handler in the
	// client is enough.
	for _, path := range []string{"/api/v1/me", "/api/v1/orders", "/api/v1/me/notifications", "/api/v1/me/inquiries"} {
		body := user.do(http.MethodGet, path, nil, http.StatusUnauthorized)
		failure, _ := body["error"].(map[string]any)
		if failure == nil || failure["code"] != "authentication_required" {
			t.Fatalf("%s 만료 응답=%v", path, body)
		}
	}

	// Signing in again on the same client works: the expired cookie is replaced
	// rather than leaving the account stuck.
	user.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23"}, http.StatusOK)
	user.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)
}

// TestIntegrationOperatorCanInvestigateOneAccount covers the screen that did
// not exist: to look into a complaint an operator had to read the user list,
// the order list, the dispute queue, the report queue and the audit log, and
// hold the answer in their head.
func TestIntegrationOperatorCanInvestigateOneAccount(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("caseop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	seller, talentID, sellerName := sellTalent(t, server, "caseseller", uniqueName("조사 상품"), 70_000)
	buyerName := uniqueName("casebuyer")
	buyer := newClient(t, server.URL)
	buyer.register(buyerName)
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "납품물이 요구사항과 다릅니다", "evidence": []any{}}, http.StatusCreated)

	var sellerID, buyerID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, sellerName).Scan(&sellerID); err != nil {
		t.Fatalf("판매자 조회: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, buyerName).Scan(&buyerID); err != nil {
		t.Fatalf("구매자 조회: %v", err)
	}

	// The seller's file shows the trade from their side and the dispute as
	// something raised against them, not by them.
	detail := operator.do(http.MethodGet, "/api/v1/admin/users/"+sellerID.String(), nil, http.StatusOK)
	orders := detail["orders"].(map[string]any)
	if orders["sold"].(float64) != 1 || orders["bought"].(float64) != 0 {
		t.Fatalf("판매자 주문 집계=%v", orders)
	}
	trouble := detail["trouble"].(map[string]any)
	if trouble["disputes_against"].(float64) != 1 || trouble["disputes_opened"].(float64) != 0 {
		t.Fatalf("판매자 분쟁 집계=%v", trouble)
	}
	if detail["seller"] == nil {
		t.Fatal("판매자 프로필이 비어 있습니다")
	}
	money := detail["money"].(map[string]any)
	if money["paid"].(float64) != 0 {
		t.Fatalf("판매자를 구매자로 집계했습니다: %v", money)
	}

	// The same dispute appears on the buyer's file as one they opened, and the
	// money they paid is counted there.
	buyerDetail := operator.do(http.MethodGet, "/api/v1/admin/users/"+buyerID.String(), nil, http.StatusOK)
	if buyerDetail["trouble"].(map[string]any)["disputes_opened"].(float64) != 1 {
		t.Fatalf("구매자 분쟁 집계=%v", buyerDetail["trouble"])
	}
	if buyerDetail["money"].(map[string]any)["paid"].(float64) != 70_000 {
		t.Fatalf("구매자 결제 총액=%v", buyerDetail["money"])
	}

	// Only an account that exists, and only for someone allowed to look.
	operator.do(http.MethodGet, "/api/v1/admin/users/"+uuid.New().String(), nil, http.StatusNotFound)
	buyer.do(http.MethodGet, "/api/v1/admin/users/"+sellerID.String(), nil, http.StatusForbidden)
}

// TestIntegrationAuditLogAnswersWhoChangedThis covers a log that had no filters
// at all: the question it exists to answer could only be reached by paging
// through every action ever recorded until you saw the one you wanted.
func TestIntegrationAuditLogAnswersWhoChangedThis(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("auditop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	coupon := operator.do(http.MethodPost, "/api/v1/admin/coupons", map[string]any{
		"code": strings.ToUpper(uniqueName("AUD"))[:12], "name": "감사 검증 쿠폰", "discount_type": "fixed",
		"discount_value": 1000, "min_order_amount": 0, "per_user_limit": 1, "active": true,
	}, http.StatusCreated)
	couponID := fmt.Sprint(coupon["id"])

	var operatorID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, operatorName).Scan(&operatorID); err != nil {
		t.Fatalf("운영자 조회: %v", err)
	}

	// Who touched this one coupon.
	byResource := operator.do(http.MethodGet, "/api/v1/admin/audit?resource_type=coupon&resource_id="+couponID, nil, http.StatusOK)["items"].([]any)
	if len(byResource) != 1 {
		t.Fatalf("쿠폰 관련 감사 기록 %d건", len(byResource))
	}
	if fmt.Sprint(byResource[0].(map[string]any)["actor_user_id"]) != operatorID.String() {
		t.Fatalf("행위자가 다릅니다: %v", byResource[0])
	}

	// What a kind of action looks like, by prefix, because the actions are
	// named coupon.create, coupon.update and so on.
	byAction := operator.do(http.MethodGet, "/api/v1/admin/audit?action=coupon", nil, http.StatusOK)["items"].([]any)
	if len(byAction) == 0 {
		t.Fatal("작업 접두사 필터가 아무것도 찾지 못했습니다")
	}
	for _, row := range byAction {
		if action := fmt.Sprint(row.(map[string]any)["action"]); !strings.HasPrefix(action, "coupon") {
			t.Fatalf("접두사와 무관한 기록이 섞였습니다: %s", action)
		}
	}

	// And everything this account did.
	byActor := operator.do(http.MethodGet, "/api/v1/admin/audit?actor="+operatorID.String(), nil, http.StatusOK)["items"].([]any)
	if len(byActor) == 0 {
		t.Fatal("행위자 필터가 아무것도 찾지 못했습니다")
	}
	// A filter that matches nothing returns nothing rather than everything.
	empty := operator.do(http.MethodGet, "/api/v1/admin/audit?action=nonexistent.action", nil, http.StatusOK)["items"].([]any)
	if len(empty) != 0 {
		t.Fatalf("없는 작업 필터가 %d건을 돌려줬습니다", len(empty))
	}
}

// TestIntegrationOperatorQueuesAreSearchable covers the queues an operator is
// asked about by name. Answering "my order KK-… is stuck" used to mean paging
// through every order the platform had taken until you saw it, and pivoting
// from a person to their orders was not possible at all.
func TestIntegrationOperatorQueuesAreSearchable(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("queueop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	title := uniqueName("검색 대상 상품")
	seller, talentID, sellerName := sellTalent(t, server, "queueseller2", title, 40_000)
	buyerName := uniqueName("queuebuyer2")
	buyer := newClient(t, server.URL)
	buyer.register(buyerName)
	order := buyer.do(http.MethodPost, "/api/v1/orders", map[string]any{"talent_id": talentID, "requirements": map[string]any{"요구사항": "검증"}, "options": []any{}}, http.StatusCreated)
	orderNumber := fmt.Sprint(order["order_number"])
	orderID := fmt.Sprint(order["id"])

	ids := func(page map[string]any) []string {
		out := []string{}
		for _, row := range page["items"].([]any) {
			out = append(out, fmt.Sprint(row.(map[string]any)["id"]))
		}
		return out
	}
	contains := func(list []string, want string) bool {
		for _, item := range list {
			if item == want {
				return true
			}
		}
		return false
	}

	// The number someone reads off their screen finds the order.
	found := ids(operator.do(http.MethodGet, "/api/v1/admin/orders?q="+url.QueryEscape(orderNumber), nil, http.StatusOK))
	if len(found) != 1 || found[0] != orderID {
		t.Fatalf("주문 번호 검색 결과=%v", found)
	}
	// So does the name of the thing they bought.
	if byTitle := ids(operator.do(http.MethodGet, "/api/v1/admin/orders?q="+url.QueryEscape(title), nil, http.StatusOK)); !contains(byTitle, orderID) {
		t.Fatalf("상품명 검색이 주문을 찾지 못했습니다: %v", byTitle)
	}

	// And a person leads to their orders, from either side of the trade.
	var buyerID, sellerID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, buyerName).Scan(&buyerID); err != nil {
		t.Fatalf("구매자 조회: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, sellerName).Scan(&sellerID); err != nil {
		t.Fatalf("판매자 조회: %v", err)
	}
	for label, id := range map[string]uuid.UUID{"구매자": buyerID, "판매자": sellerID} {
		if byUser := ids(operator.do(http.MethodGet, "/api/v1/admin/orders?user="+id.String(), nil, http.StatusOK)); !contains(byUser, orderID) {
			t.Fatalf("%s 계정으로 주문을 찾지 못했습니다: %v", label, byUser)
		}
	}

	// State narrows rather than replaces, and a state the order is not in
	// returns nothing rather than everything.
	if open := ids(operator.do(http.MethodGet, "/api/v1/admin/orders?user="+buyerID.String()+"&state=OPEN", nil, http.StatusOK)); !contains(open, orderID) {
		t.Fatalf("진행 중 필터가 주문을 놓쳤습니다: %v", open)
	}
	if done := ids(operator.do(http.MethodGet, "/api/v1/admin/orders?user="+buyerID.String()+"&state=COMPLETED", nil, http.StatusOK)); len(done) != 0 {
		t.Fatalf("완료 필터에 진행 중 주문이 나왔습니다: %v", done)
	}
	if none := ids(operator.do(http.MethodGet, "/api/v1/admin/orders?q=KK-NOT-A-REAL-NUMBER", nil, http.StatusOK)); len(none) != 0 {
		t.Fatalf("없는 주문 번호가 %d건을 돌려줬습니다", len(none))
	}

	// The talent queue takes the same kind of question.
	byStatus := operator.do(http.MethodGet, "/api/v1/admin/talents?seller="+sellerID.String()+"&status=published", nil, http.StatusOK)["items"].([]any)
	if len(byStatus) != 1 {
		t.Fatalf("판매자·상태 필터 결과=%d건", len(byStatus))
	}
	if draftOnly := operator.do(http.MethodGet, "/api/v1/admin/talents?seller="+sellerID.String()+"&status=draft", nil, http.StatusOK)["items"].([]any); len(draftOnly) != 0 {
		t.Fatalf("초안 필터에 공개 상품이 나왔습니다: %d건", len(draftOnly))
	}

	// And so does the settlement queue, once there is a settlement to find.
	payAndDeliver(t, buyer, seller, talentID)
	settlements := operator.do(http.MethodGet, "/api/v1/admin/settlements?seller="+sellerID.String(), nil, http.StatusOK)["items"].([]any)
	_ = settlements
	if wrongState := operator.do(http.MethodGet, "/api/v1/admin/settlements?state=completed&seller="+sellerID.String(), nil, http.StatusOK)["items"].([]any); len(wrongState) != 0 {
		t.Fatalf("지급 완료 필터에 %d건이 나왔습니다", len(wrongState))
	}
}

// TestIntegrationOperatorNotesOutliveTheShift covers what the audit log cannot
// hold: the judgement behind an action, and the decision not to act at all.
// "Looked into this, nothing wrong" is the most expensive thing to lose,
// because the next operator repeats the whole investigation to reach it again.
func TestIntegrationOperatorNotesOutliveTheShift(t *testing.T) {
	server, pool := integrationServer(t)
	newOperator := func(prefix, role string) *client {
		name := uniqueName(prefix)
		operator := newClient(t, server.URL)
		operator.register(name)
		grantRole(t, pool, name, role)
		operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
		operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": name, "password": "IntegrationPass!23"}, http.StatusOK)
		return operator
	}
	first := newOperator("noteop1", "operator")
	second := newOperator("noteop2", "operator")

	subjectName := uniqueName("notesubject")
	subject := newClient(t, server.URL)
	subject.register(subjectName)
	var subjectID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, subjectName).Scan(&subjectID); err != nil {
		t.Fatalf("대상 조회: %v", err)
	}

	first.do(http.MethodPost, "/api/v1/admin/notes", map[string]any{
		"subject_type": "user", "subject_id": subjectID.String(),
		"body": "신고 확인함. 배송 지연은 구매자 요청에 따른 것이라 조치하지 않음.",
	}, http.StatusCreated)
	first.do(http.MethodPost, "/api/v1/admin/notes", map[string]any{
		"subject_type": "user", "subject_id": subjectID.String(),
		"body": "이 계정은 결제 수단 확인이 끝난 상태입니다.", "pinned": true,
	}, http.StatusCreated)

	// The next operator on shift sees both, with the standing context first.
	notes := second.do(http.MethodGet, "/api/v1/admin/notes?subject_type=user&subject_id="+subjectID.String(), nil, http.StatusOK)["items"].([]any)
	if len(notes) != 2 {
		t.Fatalf("메모 %d건", len(notes))
	}
	top := notes[0].(map[string]any)
	if top["pinned"] != true {
		t.Fatalf("고정 메모가 위로 오지 않았습니다: %v", top)
	}
	if top["author_name"] == nil {
		t.Fatalf("작성자가 없습니다: %v", top)
	}

	// A colleague cannot quietly remove someone else's judgement, but the
	// author can take back their own.
	running := notes[1].(map[string]any)
	second.do(http.MethodDelete, "/api/v1/admin/notes/"+fmt.Sprint(running["id"]), nil, http.StatusForbidden)
	first.do(http.MethodDelete, "/api/v1/admin/notes/"+fmt.Sprint(running["id"]), nil, http.StatusNoContent)
	if after := second.do(http.MethodGet, "/api/v1/admin/notes?subject_type=user&subject_id="+subjectID.String(), nil, http.StatusOK)["items"].([]any); len(after) != 1 {
		t.Fatalf("삭제 후 메모 %d건", len(after))
	}

	// The notes are an operator surface, and the subject of them is not an
	// operator.
	subject.do(http.MethodGet, "/api/v1/admin/notes?subject_type=user&subject_id="+subjectID.String(), nil, http.StatusForbidden)
	subject.do(http.MethodPost, "/api/v1/admin/notes", map[string]any{
		"subject_type": "user", "subject_id": subjectID.String(), "body": "제 계정에 제가 메모",
	}, http.StatusForbidden)

	// A note has to be about something the console actually works on.
	first.do(http.MethodPost, "/api/v1/admin/notes", map[string]any{
		"subject_type": "invoice", "subject_id": subjectID.String(), "body": "알 수 없는 대상",
	}, http.StatusBadRequest)
	first.do(http.MethodPost, "/api/v1/admin/notes", map[string]any{
		"subject_type": "user", "subject_id": subjectID.String(), "body": "짧",
	}, http.StatusBadRequest)
}

// TestIntegrationDisputeCaseShowsBothSides covers the screen that decides where
// money goes. The queue used to show the reason the opener typed and nothing
// else: no deliveries, no conversation, no timeline, no sense of whether
// either party had been here before.
func TestIntegrationDisputeCaseShowsBothSides(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("disputeop2")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	seller, talentID, _ := sellTalent(t, server, "casedisputeseller", uniqueName("분쟁 사건 상품"), 150_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("casedisputebuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/messages", map[string]any{"body": "요청한 색상이 아닙니다."}, http.StatusCreated)
	seller.do(http.MethodPost, "/api/v1/orders/"+orderID+"/messages", map[string]any{"body": "요구사항에 색상 지정이 없었습니다."}, http.StatusCreated)
	dispute := buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "색상이 요청과 다릅니다", "evidence": []any{}}, http.StatusCreated)

	detail := operator.do(http.MethodGet, "/api/v1/admin/disputes/"+fmt.Sprint(dispute["id"]), nil, http.StatusOK)

	// Both accounts of what happened, not just the one that opened the case.
	messages := detail["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("대화 %d건", len(messages))
	}
	sides := map[string]bool{}
	for _, row := range messages {
		sides[fmt.Sprint(row.(map[string]any)["side"])] = true
	}
	if !sides["buyer"] || !sides["seller"] {
		t.Fatalf("한쪽 이야기만 보입니다: %v", sides)
	}

	// What was actually handed over, and what happened when.
	if deliveries := detail["deliveries"].([]any); len(deliveries) == 0 {
		t.Fatal("납품물이 사건 화면에 없습니다")
	}
	if timeline := detail["timeline"].([]any); len(timeline) < 3 {
		t.Fatalf("주문 이력이 %d건뿐입니다", len(timeline))
	}

	// The money actually at stake, rather than the order's face value.
	order := detail["order"].(map[string]any)
	if order["escrow"].(float64) != 150_000 {
		t.Fatalf("에스크로 잔액=%v", order["escrow"])
	}

	// And whether either of these two has been here before.
	if detail["buyer"].(map[string]any)["disputes_opened"].(float64) != 1 {
		t.Fatalf("구매자 이력=%v", detail["buyer"])
	}
	if detail["seller"].(map[string]any)["disputes_against"].(float64) != 1 {
		t.Fatalf("판매자 이력=%v", detail["seller"])
	}
	if detail["opened_by"].(map[string]any)["side"] != "buyer" {
		t.Fatalf("신청자 구분=%v", detail["opened_by"])
	}

	// It is an operator surface, and a missing case says so plainly.
	buyer.do(http.MethodGet, "/api/v1/admin/disputes/"+fmt.Sprint(dispute["id"]), nil, http.StatusForbidden)
	operator.do(http.MethodGet, "/api/v1/admin/disputes/"+uuid.New().String(), nil, http.StatusNotFound)
}

// TestIntegrationDisputeCaseShowsWhatCanBeReclaimed covers a figure that would
// have lied at the worst moment. An accepted order that is then disputed has
// already moved its escrow into a settlement, so the escrow balance reads zero
// while the operator decides where that same money goes.
func TestIntegrationDisputeCaseShowsWhatCanBeReclaimed(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("reclaimop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	seller, talentID, _ := sellTalent(t, server, "reclaimseller", uniqueName("회수 검증 상품"), 120_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("reclaimbuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)

	// Before acceptance the money is in escrow and both figures agree.
	beforeDispute := buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "납품물 확인이 필요합니다"}, http.StatusCreated)
	delivered := operator.do(http.MethodGet, "/api/v1/admin/disputes/"+fmt.Sprint(beforeDispute["id"]), nil, http.StatusOK)["order"].(map[string]any)
	if delivered["escrow"].(float64) != 120_000 || delivered["reclaimable"].(float64) != 120_000 {
		t.Fatalf("납품 단계 금액=%v", delivered)
	}

	// Accepting moves the same money into a settlement. A dispute raised now
	// must still show what resolving it can pull back.
	operator.do(http.MethodPost, "/api/v1/admin/disputes/"+fmt.Sprint(beforeDispute["id"])+"/resolve", map[string]any{
		"outcome": "release_to_seller", "refund_amount": 0, "note": "확인 완료",
	}, http.StatusOK)

	seller2, talentID2, _ := sellTalent(t, server, "reclaimseller2", uniqueName("회수 검증 상품2"), 90_000)
	buyer2 := newClient(t, server.URL)
	buyer2.register(uniqueName("reclaimbuyer2"))
	orderID2 := payAndDeliver(t, buyer2, seller2, talentID2)
	buyer2.do(http.MethodPost, "/api/v1/orders/"+orderID2+"/accept", map[string]any{}, http.StatusOK)
	// Opening the dispute is what moves an accepted order into DISPUTED; the
	// buyer cannot make that transition by hand.
	accepted := buyer2.do(http.MethodPost, "/api/v1/orders/"+orderID2+"/disputes", map[string]any{"reason": "확정 후 결함을 발견했습니다"}, http.StatusCreated)

	afterAccept := operator.do(http.MethodGet, "/api/v1/admin/disputes/"+fmt.Sprint(accepted["id"]), nil, http.StatusOK)["order"].(map[string]any)
	if afterAccept["escrow"].(float64) != 0 {
		t.Fatalf("구매확정 후 에스크로=%v (0이어야 합니다)", afterAccept["escrow"])
	}
	// The number the operator decides against is this one, not the zero above.
	if afterAccept["reclaimable"].(float64) != 90_000 {
		t.Fatalf("되돌릴 수 있는 금액=%v", afterAccept["reclaimable"])
	}
	if afterAccept["settlement_state"] == nil {
		t.Fatalf("정산 상태가 없습니다: %v", afterAccept)
	}
}

// TestIntegrationOptionalListFieldsMayBeOmitted covers a shape that fails in an
// unhelpful way: several columns are NOT NULL with a default, and a caller who
// leaves the matching optional field out of the JSON sends SQL NULL into them
// and gets an opaque 500. Every client we ship happens to send an empty list,
// so the obvious minimal request was the one nobody tried.
func TestIntegrationOptionalListFieldsMayBeOmitted(t *testing.T) {
	server, _ := integrationServer(t)
	seller, talentID, _ := sellTalent(t, server, "minimalseller", uniqueName("최소 요청 상품"), 50_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("minimalbuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)

	// A message with only a body.
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/messages", map[string]any{"body": "첨부 없이 보냅니다."}, http.StatusCreated)

	// A revision request with only the details.
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/revision", map[string]any{"details": "색상을 요청대로 바꿔 주세요."}, http.StatusCreated)

	// A dispute with only a reason.
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "요청과 다른 결과물입니다"}, http.StatusCreated)

	// A seller profile with no skills and no settings.
	fresh := newClient(t, server.URL)
	fresh.register(uniqueName("minimalprofile"))
	fresh.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "최소 입력", "biography": "", "capacity": 3,
	}, http.StatusOK)

	// An API key with neither scopes nor an address allowlist.
	fresh.do(http.MethodPost, "/api/v1/me/api-keys", map[string]any{"name": "최소 키", "rate_limit_per_minute": 60}, http.StatusCreated)
}

// TestIntegrationSettlementHoldReachesTheSeller covers money stopping without
// a word. An operator holding a payout, and releasing it again, both happened
// in silence: the seller could see the state change on their earnings page if
// they happened to look, and was told nothing otherwise. The templates for
// both had existed since the beginning and were never reached.
func TestIntegrationSettlementHoldReachesTheSeller(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("holdop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	seller, talentID, _ := sellTalent(t, server, "holdseller", uniqueName("보류 상품"), 200_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("holdbuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/accept", map[string]any{}, http.StatusOK)

	settlements := operator.do(http.MethodGet, "/api/v1/admin/settlements?limit=200", nil, http.StatusOK)["items"].([]any)
	settlementID := ""
	for _, row := range settlements {
		item := row.(map[string]any)
		if fmt.Sprint(item["order_id"]) == orderID {
			settlementID = fmt.Sprint(item["id"])
		}
	}
	if settlementID == "" {
		t.Fatal("구매확정 후 정산이 만들어지지 않았습니다")
	}

	reason := "신원 확인 서류 미제출"
	operator.do(http.MethodPost, "/api/v1/admin/settlements/"+settlementID+"/action", map[string]any{"action": "hold", "reason": reason}, http.StatusOK)

	// The seller is told, and told why — the reason is the whole point of the
	// message.
	eventually(t, "정산 보류 알림", 20*time.Second, func() bool {
		for _, row := range seller.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
			item := row.(map[string]any)
			if item["event_type"] == "SettlementHeld" && strings.Contains(fmt.Sprint(item["body"]), reason) {
				return true
			}
		}
		return false
	})

	// And told again when their money starts moving.
	operator.do(http.MethodPost, "/api/v1/admin/settlements/"+settlementID+"/action", map[string]any{"action": "release", "reason": ""}, http.StatusOK)
	eventually(t, "정산 재개 알림", 20*time.Second, func() bool {
		for _, row := range seller.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
			if row.(map[string]any)["event_type"] == "SettlementConfirmed" {
				return true
			}
		}
		return false
	})

	// The buyer has no stake in the seller's payout schedule.
	for _, row := range buyer.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
		if event := fmt.Sprint(row.(map[string]any)["event_type"]); event == "SettlementHeld" || event == "SettlementConfirmed" {
			t.Fatalf("구매자에게 정산 알림이 갔습니다: %s", event)
		}
	}
}

// TestIntegrationAccountChangesReachTheirOwner covers operator actions that
// change someone's account under them. The MFA reset is the one that matters
// most: quietly removing a target's second factor is a step in taking their
// account, and the only person who could tell whether it was asked for is the
// person nobody told.
func TestIntegrationAccountChangesReachTheirOwner(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("noticeop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	ownerName := uniqueName("noticeowner")
	owner := newClient(t, server.URL)
	owner.register(ownerName)
	var ownerID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, ownerName).Scan(&ownerID); err != nil {
		t.Fatalf("계정 조회: %v", err)
	}
	received := func(eventType string) func() bool {
		return func() bool {
			for _, row := range owner.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
				if row.(map[string]any)["event_type"] == eventType {
					return true
				}
			}
			return false
		}
	}

	// A second factor removed without a word is the worst of these, so there
	// has to be one to remove.
	setup := owner.do(http.MethodPost, "/api/v1/me/mfa/totp/setup", map[string]any{}, http.StatusCreated)
	owner.do(http.MethodPost, "/api/v1/me/mfa/totp/confirm", map[string]any{"code": totpFor(t, fmt.Sprint(setup["secret"]), time.Now())}, http.StatusOK)
	operator.do(http.MethodDelete, "/api/v1/admin/users/"+ownerID.String()+"/mfa", nil, http.StatusOK)
	// The reset ends every session, so reading the notice means signing in
	// again — which is exactly what the person is meant to do.
	owner.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": ownerName, "password": "IntegrationPass!23"}, http.StatusOK)
	eventually(t, "MFA 해제 알림", 20*time.Second, received("AccountMFAReset"))

	// Roles change what an account may do, and the notice says what they are now.
	operator.do(http.MethodPut, "/api/v1/admin/users/"+ownerID.String()+"/roles", map[string]any{"roles": []string{"buyer"}}, http.StatusOK)
	eventually(t, "역할 변경 알림", 20*time.Second, func() bool {
		for _, row := range owner.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
			item := row.(map[string]any)
			if item["event_type"] == "AccountRoleChanged" && strings.Contains(fmt.Sprint(item["body"]), "buyer") {
				return true
			}
		}
		return false
	})

	// An integration that stops working should say why it stopped.
	created := owner.do(http.MethodPost, "/api/v1/me/api-keys", map[string]any{"name": "연동 키", "rate_limit_per_minute": 60}, http.StatusCreated)
	keys := operator.do(http.MethodGet, "/api/v1/admin/users/"+ownerID.String()+"/api-keys", nil, http.StatusOK)["items"].([]any)
	if len(keys) == 0 {
		t.Fatalf("키를 찾지 못했습니다 (생성=%v)", created["id"])
	}
	operator.do(http.MethodDelete, "/api/v1/admin/api-keys/"+fmt.Sprint(keys[0].(map[string]any)["id"]), nil, http.StatusNoContent)
	eventually(t, "API 키 폐기 알림", 20*time.Second, func() bool {
		for _, row := range owner.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
			item := row.(map[string]any)
			if item["event_type"] == "APIKeyRevoked" && strings.Contains(fmt.Sprint(item["body"]), "연동 키") {
				return true
			}
		}
		return false
	})

	// These are about one account and go nowhere else.
	bystander := newClient(t, server.URL)
	bystander.register(uniqueName("noticebystander"))
	for _, row := range bystander.do(http.MethodGet, "/api/v1/me/notifications", nil, http.StatusOK)["items"].([]any) {
		if event := fmt.Sprint(row.(map[string]any)["event_type"]); strings.HasPrefix(event, "Account") || event == "APIKeyRevoked" {
			t.Fatalf("무관한 계정에 알림이 갔습니다: %s", event)
		}
	}
}

// TestIntegrationOrderCaseGivesTheQueueSomewhereToLead covers what happened
// after an operator found the order they were asked about: nothing. The card
// showed a state and stopped, with no timeline, no deliveries, no conversation
// and nowhere to record what was found.
func TestIntegrationOrderCaseGivesTheQueueSomewhereToLead(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("ordercaseop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	seller, talentID, _ := sellTalent(t, server, "ordercaseseller", uniqueName("주문 사건 상품"), 130_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("ordercasebuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/messages", map[string]any{"body": "확인 부탁드립니다."}, http.StatusCreated)

	detail := operator.do(http.MethodGet, "/api/v1/admin/orders/"+orderID, nil, http.StatusOK)

	// The evidence an operator needs to answer "what happened here".
	if len(detail["timeline"].([]any)) < 3 {
		t.Fatalf("주문 이력 %d건", len(detail["timeline"].([]any)))
	}
	if len(detail["deliveries"].([]any)) == 0 {
		t.Fatal("납품물이 없습니다")
	}
	if len(detail["messages"].([]any)) != 1 {
		t.Fatalf("대화 %d건", len(detail["messages"].([]any)))
	}
	// Money as it stands, not as it was agreed.
	sums := detail["money"].(map[string]any)
	if sums["paid"].(float64) != 130_000 || sums["escrow"].(float64) != 130_000 || sums["refunded"].(float64) != 0 {
		t.Fatalf("금액=%v", sums)
	}

	// And a place to record what was found, which the next person will read.
	operator.do(http.MethodPost, "/api/v1/admin/notes", map[string]any{
		"subject_type": "order", "subject_id": orderID, "body": "납품물은 요구사항대로입니다. 구매자에게 확인 요청 안내했습니다.",
	}, http.StatusCreated)
	notes := operator.do(http.MethodGet, "/api/v1/admin/notes?subject_type=order&subject_id="+orderID, nil, http.StatusOK)["items"].([]any)
	if len(notes) != 1 {
		t.Fatalf("주문 메모 %d건", len(notes))
	}

	// It is an operator surface even though the buyer owns the order.
	buyer.do(http.MethodGet, "/api/v1/admin/orders/"+orderID, nil, http.StatusForbidden)
	operator.do(http.MethodGet, "/api/v1/admin/orders/"+uuid.New().String(), nil, http.StatusNotFound)
}

// TestIntegrationDashboardCountsWorkThatCanBeOpened covers the console's front
// door. It used to report five counts in one sentence with a single link, so an
// operator read "분쟁 3건" and then went to find those three themselves. Each
// count now has a queue that can be narrowed to exactly those items, which only
// holds if the counts and the filters agree.
func TestIntegrationDashboardCountsWorkThatCanBeOpened(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("boardop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	initial := operator.do(http.MethodGet, "/api/v1/admin/dashboard", nil, http.StatusOK)
	before := initial["overdue_orders"].(float64)
	paidBefore := initial["gmv"].(float64)

	seller, talentID, _ := sellTalent(t, server, "boardseller", uniqueName("대시보드 상품"), 60_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("boardbuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	if _, err := pool.Exec(context.Background(), `UPDATE orders SET due_at=now()-interval '3 days' WHERE id=$1`, orderID); err != nil {
		t.Fatalf("납기 변경: %v", err)
	}

	after := operator.do(http.MethodGet, "/api/v1/admin/dashboard", nil, http.StatusOK)
	if after["overdue_orders"].(float64) != before+1 {
		t.Fatalf("납기 초과 집계=%v (이전 %v)", after["overdue_orders"], before)
	}
	// "누적 결제" summed a payment state the checkout never writes, so it read
	// 0원 with money in escrow. It has to grow by exactly what the order case
	// says was paid — the two screens read the same rows.
	paid := operator.do(http.MethodGet, "/api/v1/admin/orders/"+orderID, nil, http.StatusOK)["money"].(map[string]any)["paid"].(float64)
	if paid <= 0 {
		t.Fatalf("결제 금액=%v", paid)
	}
	if after["gmv"].(float64) != paidBefore+paid {
		t.Fatalf("누적 결제=%v (이전 %v, 결제 %v)", after["gmv"], paidBefore, paid)
	}

	// The number the board shows and the list its link opens have to be the
	// same set, or the front door sends people somewhere that disagrees with it.
	listed := operator.do(http.MethodGet, "/api/v1/admin/orders?overdue=1&limit=500", nil, http.StatusOK)["items"].([]any)
	if float64(len(listed)) != after["overdue_orders"].(float64) {
		t.Fatalf("대시보드는 %v건인데 대기열은 %d건입니다", after["overdue_orders"], len(listed))
	}
	found := false
	for _, row := range listed {
		if fmt.Sprint(row.(map[string]any)["id"]) == orderID {
			found = true
		}
	}
	if !found {
		t.Fatal("납기 초과 필터가 해당 주문을 담지 않았습니다")
	}

	// The settlement link narrows to holds, and says so honestly when empty.
	holds := operator.do(http.MethodGet, "/api/v1/admin/settlements?state=hold&limit=500", nil, http.StatusOK)["items"].([]any)
	if float64(len(holds)) != after["settlement_holds"].(float64) {
		t.Fatalf("대시보드 보류 %v건, 목록 %d건", after["settlement_holds"], len(holds))
	}
}

// TestIntegrationReportCaseShowsWhatWasReported covers the screen behind an
// enforcement decision. From the queue an operator could hide a listing or
// suspend an account while seeing only the reporter's words and the name of
// the thing they named.
func TestIntegrationReportCaseShowsWhatWasReported(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("reportcaseop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	description := "이 설명에 신고 사유가 될 만한 문구가 들어 있습니다."
	sellerName := uniqueName("reportcaseseller")
	seller := newClient(t, server.URL)
	seller.register(sellerName)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "신고 검증", "biography": "", "capacity": 5,
	}, http.StatusOK)
	talent := seller.do(http.MethodPost, "/api/v1/talents", talentPayload(uniqueName("신고 대상 상품"), description, 40_000), http.StatusCreated)
	talentID := fmt.Sprint(talent["id"])
	seller.do(http.MethodPost, "/api/v1/talents/"+talentID+"/publish", nil, http.StatusOK)

	reporter := newClient(t, server.URL)
	reporter.register(uniqueName("reportcasereporter"))
	first := reporter.do(http.MethodPost, "/api/v1/reports", map[string]any{
		"resource_type": "talent", "resource_id": talentID, "reason": "inappropriate", "details": "금지된 서비스로 보입니다.",
	}, http.StatusCreated)
	// A second reporter on the same listing is the pattern an operator needs.
	other := newClient(t, server.URL)
	other.register(uniqueName("reportcaseother"))
	other.do(http.MethodPost, "/api/v1/reports", map[string]any{
		"resource_type": "talent", "resource_id": talentID, "reason": "inappropriate", "details": "저도 같은 문제를 봤습니다.",
	}, http.StatusCreated)

	detail := operator.do(http.MethodGet, "/api/v1/admin/reports/"+fmt.Sprint(first["id"]), nil, http.StatusOK)

	// The thing that was reported, not just its name.
	subject, ok := detail["subject"].(map[string]any)
	if !ok || subject["kind"] != "talent" {
		t.Fatalf("신고 대상=%v", detail["subject"])
	}
	if subject["description"] != description {
		t.Fatalf("신고된 내용이 화면에 없습니다: %v", subject["description"])
	}
	if subject["seller"].(map[string]any)["display_name"] != sellerName {
		t.Fatalf("대상 소유자=%v", subject["seller"])
	}

	// Who is reporting, and how often they do it.
	whoReported := detail["reporter"].(map[string]any)
	if whoReported["reports_filed"].(float64) != 1 || whoReported["reports_open"].(float64) != 1 {
		t.Fatalf("신고자 이력=%v", whoReported)
	}

	// And whether anyone else has said the same thing.
	history := detail["history"].([]any)
	if len(history) != 1 {
		t.Fatalf("같은 대상의 다른 신고 %d건", len(history))
	}

	// A reported message cannot be judged without the message. This is the case
	// where a name and a reason tell an operator nothing at all.
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("reportcasebuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/messages", map[string]any{"body": "여기 신고 대상이 되는 문구가 있습니다."}, http.StatusCreated)
	var messageID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM messages WHERE order_id=$1 ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&messageID); err != nil {
		t.Fatalf("메시지 조회: %v", err)
	}
	messageReport := seller.do(http.MethodPost, "/api/v1/reports", map[string]any{
		"resource_type": "message", "resource_id": messageID.String(), "reason": "inappropriate", "details": "부적절한 표현입니다.",
	}, http.StatusCreated)
	messageCase := operator.do(http.MethodGet, "/api/v1/admin/reports/"+fmt.Sprint(messageReport["id"]), nil, http.StatusOK)
	messageSubject, ok := messageCase["subject"].(map[string]any)
	if !ok || messageSubject["kind"] != "message" {
		t.Fatalf("메시지 신고 대상=%v", messageCase["subject"])
	}
	if messageSubject["body"] != "여기 신고 대상이 되는 문구가 있습니다." {
		t.Fatalf("신고된 메시지 본문이 없습니다: %v", messageSubject["body"])
	}
	if messageSubject["sender"].(map[string]any)["display_name"] == nil || messageSubject["order_number"] == nil {
		t.Fatalf("메시지 맥락이 없습니다: %v", messageSubject)
	}

	// It is an operator surface.
	reporter.do(http.MethodGet, "/api/v1/admin/reports/"+fmt.Sprint(first["id"]), nil, http.StatusForbidden)
	operator.do(http.MethodGet, "/api/v1/admin/reports/"+uuid.New().String(), nil, http.StatusNotFound)
}

// TestIntegrationQueuesShowWhatHasBeenLookedAt covers the half of operator
// notes that makes them work. A note nobody can see from the queue does not
// stop the next person opening the same case and investigating it again, which
// is the whole reason the notes exist.
func TestIntegrationQueuesShowWhatHasBeenLookedAt(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("noteflagop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	seller, talentID, _ := sellTalent(t, server, "noteflagseller", uniqueName("메모 표시 상품"), 55_000)
	buyer := newClient(t, server.URL)
	buyer.register(uniqueName("noteflagbuyer"))
	orderID := payAndDeliver(t, buyer, seller, talentID)
	dispute := buyer.do(http.MethodPost, "/api/v1/orders/"+orderID+"/disputes", map[string]any{"reason": "확인이 필요합니다"}, http.StatusCreated)
	report := buyer.do(http.MethodPost, "/api/v1/reports", map[string]any{
		"resource_type": "talent", "resource_id": talentID, "reason": "inappropriate", "details": "확인 요청",
	}, http.StatusCreated)

	countFor := func(path, id string) float64 {
		for _, row := range operator.do(http.MethodGet, path, nil, http.StatusOK)["items"].([]any) {
			item := row.(map[string]any)
			if fmt.Sprint(item["id"]) == id {
				count, _ := item["note_count"].(float64)
				return count
			}
		}
		t.Fatalf("%s 에서 %s 를 찾지 못했습니다", path, id)
		return -1
	}

	// Nothing has been looked at yet.
	if got := countFor("/api/v1/admin/orders?limit=200", orderID); got != 0 {
		t.Fatalf("주문 메모 표시=%v", got)
	}
	if got := countFor("/api/v1/admin/disputes?limit=200", fmt.Sprint(dispute["id"])); got != 0 {
		t.Fatalf("분쟁 메모 표시=%v", got)
	}
	if got := countFor("/api/v1/admin/reports?limit=200", fmt.Sprint(report["id"])); got != 0 {
		t.Fatalf("신고 메모 표시=%v", got)
	}

	// One operator records what they found on each.
	for _, subject := range []struct{ kind, id string }{
		{"order", orderID},
		{"dispute", fmt.Sprint(dispute["id"])},
		{"report", fmt.Sprint(report["id"])},
	} {
		operator.do(http.MethodPost, "/api/v1/admin/notes", map[string]any{
			"subject_type": subject.kind, "subject_id": subject.id, "body": "살펴봤습니다. 추가 조치 불필요.",
		}, http.StatusCreated)
	}

	// The next person can see that from the queue, without opening anything.
	if got := countFor("/api/v1/admin/orders?limit=200", orderID); got != 1 {
		t.Fatalf("메모 후 주문 표시=%v", got)
	}
	if got := countFor("/api/v1/admin/disputes?limit=200", fmt.Sprint(dispute["id"])); got != 1 {
		t.Fatalf("메모 후 분쟁 표시=%v", got)
	}
	if got := countFor("/api/v1/admin/reports?limit=200", fmt.Sprint(report["id"])); got != 1 {
		t.Fatalf("메모 후 신고 표시=%v", got)
	}
}

// TestIntegrationApprovalShowsWhatIsBeingApproved covers the gate the
// marketplace's quality rests on. The request carries only a title, so the
// review screen showed a name and a policy: not the description, not the
// price, not the packages, not who is selling. Approving content you cannot
// see is not a review.
func TestIntegrationApprovalShowsWhatIsBeingApproved(t *testing.T) {
	server, pool := integrationServer(t)
	operatorName := uniqueName("approvalcaseop")
	operator := newClient(t, server.URL)
	operator.register(operatorName)
	grantRole(t, pool, operatorName, "super_admin")
	operator.do(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent)
	operator.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": operatorName, "password": "IntegrationPass!23"}, http.StatusOK)

	policy := operator.do(http.MethodPost, "/api/v1/admin/approvals/policies", map[string]any{
		"resource_type": "talent_publish", "name": uniqueName("검토 내용 정책"), "enabled": true, "priority": 5,
		"conditions": map[string]any{"min_amount": 1_000}, "steps": []map[string]any{{"role": "operator", "min_approvals": 1}},
	}, http.StatusCreated)
	t.Cleanup(func() {
		operator.do(http.MethodPut, "/api/v1/admin/approvals/policies/"+fmt.Sprint(policy["id"]), map[string]any{
			"resource_type": "talent_publish", "name": "검토 내용 정책 종료", "enabled": false, "priority": 5,
			"conditions": map[string]any{"min_amount": 1_000}, "steps": []map[string]any{{"role": "operator", "min_approvals": 1}},
		}, http.StatusOK)
	})

	description := "이 설명은 승인 화면에 반드시 보여야 하는 본문입니다."
	sellerName := uniqueName("approvalcaseseller")
	seller := newClient(t, server.URL)
	seller.register(sellerName)
	seller.do(http.MethodPut, "/api/v1/me/seller-profile", map[string]any{
		"seller_type": "individual", "headline": "검토 대상", "biography": "", "capacity": 5,
	}, http.StatusOK)
	talent := seller.do(http.MethodPost, "/api/v1/talents", talentPayload(uniqueName("승인 검토 상품"), description, 250_000), http.StatusCreated)
	seller.do(http.MethodPost, "/api/v1/talents/"+fmt.Sprint(talent["id"])+"/publish", nil, http.StatusOK)

	requests := operator.do(http.MethodGet, "/api/v1/admin/approvals/requests", nil, http.StatusOK)["items"].([]any)
	requestID := ""
	for _, row := range requests {
		item := row.(map[string]any)
		if fmt.Sprint(item["resource_id"]) == fmt.Sprint(talent["id"]) {
			requestID = fmt.Sprint(item["id"])
		}
	}
	if requestID == "" {
		t.Fatal("공개 요청이 승인 대기열에 오지 않았습니다")
	}

	detail := operator.do(http.MethodGet, "/api/v1/admin/approvals/requests/"+requestID, nil, http.StatusOK)
	listing, ok := detail["talent"].(map[string]any)
	if !ok {
		t.Fatalf("승인 대상 상품이 없습니다: %v", detail)
	}
	// The thing being approved, not its name.
	if listing["description"] != description {
		t.Fatalf("승인 화면에 설명이 없습니다: %v", listing["description"])
	}
	if listing["base_price"].(float64) != 250_000 {
		t.Fatalf("가격=%v", listing["base_price"])
	}
	if len(listing["packages"].([]any)) == 0 {
		t.Fatal("패키지가 화면에 없습니다")
	}
	// And who is asking, with the history that bears on whether to trust it.
	sellerInfo := listing["seller"].(map[string]any)
	if sellerInfo["display_name"] != sellerName {
		t.Fatalf("판매자=%v", sellerInfo)
	}
	if _, present := sellerInfo["rejected_talents"]; !present {
		t.Fatalf("판매자 반려 이력이 없습니다: %v", sellerInfo)
	}
	if detail["policy"].(map[string]any)["name"] == nil {
		t.Fatalf("적용된 정책이 없습니다: %v", detail["policy"])
	}

	// It is an operator surface.
	seller.do(http.MethodGet, "/api/v1/admin/approvals/requests/"+requestID, nil, http.StatusForbidden)
	operator.do(http.MethodGet, "/api/v1/admin/approvals/requests/"+uuid.New().String(), nil, http.StatusNotFound)
}
