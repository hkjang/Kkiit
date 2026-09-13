package mail

import (
	"strings"
	"time"
)

// SettingKey is the system_settings row that holds everything but the
// password. The password is the row's encrypted secret, which the settings API
// reports as configured or not and never returns.
const SettingKey = "mail"

// Defaults aim at the common case: an internal relay on port 25 that accepts
// mail from the network without credentials.
const (
	defaultPort     = 25
	defaultSecurity = "auto"
	defaultTimeout  = 10 * time.Second
	defaultFromName = "Kkiit"
)

// Config is the relay connection plus the per-event switches. Username and
// password never serialise: the console reads the setting row directly and
// the password is not in it.
type Config struct {
	Enabled     bool
	Host        string
	Port        int
	Username    string
	Password    string
	FromAddress string
	FromName    string
	Security    string
	SkipVerify  bool
	BaseURL     string
	Timeout     time.Duration
	Events      map[string]bool
}

// Audience names who an event mail goes to. The event resolver knows the
// buyer and the seller of everything that has both; the mail never goes to
// whoever performed the action, whichever side they are on.
const (
	AudienceBuyer = "buyer"
	// AudienceSeller is the seller of the order, quote or settlement.
	AudienceSeller = "seller"
	// AudienceOther is the party that did not act: the counterpart of a dispute.
	AudienceOther = "other"
	// AudienceAll is both parties, minus the actor.
	AudienceAll = "all"
)

// Event is one notification people wait for. The list is deliberately short:
// a mail is worth sending only when not getting it costs somebody money or
// keeps them refreshing a page. Plain "something changed" events stay in the
// inbox.
type Event struct {
	Type     string
	Setting  string
	Audience string
	Label    string
}

// Events lists what is mailed, in the order the console shows the switches.
// Each Setting is a boolean field of the mail setting, true by default.
var Events = []Event{
	// The buyer's money is in escrow and the clock on the promised date
	// started: the seller has to notice, and the seller is not watching.
	{Type: "OrderPAID", Setting: "notify_order_paid", Audience: AudienceSeller, Label: "결제 완료 — 판매자에게 작업 시작 차례를 알림"},
	// The deliverable arrived and the acceptance window is running; a buyer
	// who does not see it loses the chance to ask for a revision.
	{Type: "OrderDELIVERED", Setting: "notify_order_delivered", Audience: AudienceBuyer, Label: "납품 도착 — 구매자에게 검수 차례를 알림"},
	// The order is back on the seller's desk with the same deadline.
	{Type: "OrderREVISION_REQUESTED", Setting: "notify_revision_requested", Audience: AudienceSeller, Label: "수정 요청 — 판매자에게 다시 작업 차례를 알림"},
	// A buyer who posted a project waits for quotes; a seller whose quote was
	// picked now has a paid order to start.
	{Type: "QuoteCreated", Setting: "notify_quote", Audience: AudienceBuyer, Label: "견적 도착 — 구매자에게"},
	{Type: "QuoteAccepted", Setting: "notify_quote", Audience: AudienceSeller, Label: "견적 선택 — 판매자에게"},
	// A dispute freezes the money. The other side has to answer, and both
	// sides wait for the decision.
	{Type: "DisputeOpened", Setting: "notify_dispute", Audience: AudienceOther, Label: "분쟁 접수 — 상대방에게"},
	{Type: "DisputeResolved", Setting: "notify_dispute", Audience: AudienceAll, Label: "분쟁 처리 — 양쪽에게"},
	// A payout that stopped is the one thing a seller cannot see from the
	// order screen.
	{Type: "SettlementHeld", Setting: "notify_settlement_held", Audience: AudienceSeller, Label: "정산 보류 — 판매자에게"},
}

// EventTest is the record type of a mail sent from the console.
const EventTest = "test"

// Lookup returns the mail event for an outbox event type.
func Lookup(eventType string) (Event, bool) {
	for _, event := range Events {
		if event.Type == eventType {
			return event, true
		}
	}
	return Event{}, false
}

// DefaultValues is the setting row as a fresh installation stores it: off,
// port 25, no security demanded, every event switch on.
func DefaultValues() map[string]any {
	values := map[string]any{
		"enabled": false, "smtp_host": "", "smtp_port": float64(defaultPort), "security": defaultSecurity, "skip_tls_verify": false,
		"username": "", "from_address": "", "from_name": defaultFromName, "base_url": "", "timeout_seconds": float64(defaultTimeout / time.Second),
	}
	for _, event := range Events {
		values[event.Setting] = true
	}
	return values
}

// ReadConfig builds the configuration from the decoded setting row and the
// decrypted password. Missing fields take the defaults so a row written by an
// older version, or by hand, still reads.
func ReadConfig(values map[string]any, password string) Config {
	config := Config{Port: defaultPort, Security: defaultSecurity, Timeout: defaultTimeout, Events: map[string]bool{}, Password: password}
	config.Enabled, _ = values["enabled"].(bool)
	config.Host = stringValue(values, "smtp_host", "")
	config.Username = stringValue(values, "username", "")
	config.FromAddress = stringValue(values, "from_address", "")
	config.FromName = stringValue(values, "from_name", defaultFromName)
	config.Security = strings.ToLower(stringValue(values, "security", defaultSecurity))
	config.BaseURL = stringValue(values, "base_url", "")
	config.SkipVerify, _ = values["skip_tls_verify"].(bool)
	if port, ok := numberValue(values, "smtp_port"); ok && port > 0 {
		config.Port = port
	}
	if seconds, ok := numberValue(values, "timeout_seconds"); ok && seconds > 0 {
		config.Timeout = time.Duration(seconds) * time.Second
	}
	// A relay on the implicit TLS port needs no extra configuration.
	if config.Security == defaultSecurity && config.Port == 465 {
		config.Security = "tls"
	}
	for _, event := range Events {
		if enabled, ok := values[event.Setting].(bool); ok {
			config.Events[event.Type] = enabled
		}
	}
	if config.FromAddress == "" && config.Host != "" {
		config.FromAddress = "kkiit@" + config.Host
	}
	return config
}

// Allows reports whether an event should be mailed. Unknown event types are
// never mailed: the list above is the whole contract.
func (c Config) Allows(eventType string) bool {
	if _, known := Lookup(eventType); !known {
		return false
	}
	if enabled, set := c.Events[eventType]; set {
		return enabled
	}
	return true
}

// Link turns an in-app path into the absolute address a mail can carry, or
// nothing when no base address is configured — a relative link in a mail is
// worse than none.
func (c Config) Link(path string) string {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" || strings.TrimSpace(path) == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return base + "/" + strings.TrimLeft(path, "/")
}

func stringValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func numberValue(values map[string]any, key string) (int, bool) {
	switch typed := values[key].(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	}
	return 0, false
}
