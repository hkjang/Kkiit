// Package mail delivers event notifications through a company SMTP relay.
//
// Internal relays commonly accept mail on port 25 with no credentials and no
// TLS, so authentication and encryption are optional and the transport adapts
// to whatever the server advertises. Nothing here runs on a request path: the
// dispatcher queues a delivery row in the same transaction as the inbox
// notification and sends it later, so a relay that is slow or down never
// slows a payment or a delivery. Every attempt is recorded so an
// administrator can see what left the building.
package mail

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// ErrInvalid marks a configuration that can never send, as opposed to a relay
// that refused this particular attempt.
var ErrInvalid = errors.New("invalid mail configuration")

// Message is one envelope: a single recipient and the already composed text.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Address is the RFC 5322 From header value.
func (c Config) Address() string {
	from := strings.TrimSpace(c.FromAddress)
	if name := strings.TrimSpace(c.FromName); name != "" {
		return fmt.Sprintf("%s <%s>", name, from)
	}
	return from
}

func (c Config) endpoint() string { return net.JoinHostPort(c.Host, fmt.Sprint(c.Port)) }

// Validate reports why the configuration cannot send. It is checked when an
// administrator saves and again before every attempt, so a row edited outside
// the console fails with a reason rather than a hang.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("%w: mail.smtp_host 가 필요합니다", ErrInvalid)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("%w: mail.smtp_port 는 1~65535 사이여야 합니다", ErrInvalid)
	}
	if !strings.Contains(c.FromAddress, "@") {
		return fmt.Errorf("%w: mail.from_address 는 메일 주소여야 합니다", ErrInvalid)
	}
	switch c.Security {
	case "auto", "none", "starttls", "tls":
	default:
		return fmt.Errorf("%w: mail.security 는 auto, none, starttls, tls 중 하나여야 합니다", ErrInvalid)
	}
	if c.Timeout < time.Second || c.Timeout > 2*time.Minute {
		return fmt.Errorf("%w: mail.timeout_seconds 는 1~120 사이여야 합니다", ErrInvalid)
	}
	return nil
}

// Deliver opens a connection and sends one message. Dialing is bounded by the
// configured timeout and the caller's context, so a relay that accepts the TCP
// connection and then says nothing cannot hold a worker forever.
func Deliver(config Config, message Message) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if !strings.Contains(message.To, "@") {
		return fmt.Errorf("%w: 받는 사람 주소가 필요합니다", ErrInvalid)
	}
	client, err := dial(config)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := startSession(client, config); err != nil {
		return err
	}
	if err := client.Mail(strings.TrimSpace(config.FromAddress)); err != nil {
		return fmt.Errorf("MAIL FROM 실패: %w", err)
	}
	if err := client.Rcpt(strings.TrimSpace(message.To)); err != nil {
		return fmt.Errorf("RCPT TO 실패: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA 실패: %w", err)
	}
	if _, err := writer.Write([]byte(compose(config, message))); err != nil {
		return fmt.Errorf("본문 전송 실패: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("본문 종료 실패: %w", err)
	}
	return client.Quit()
}

func dial(config Config) (*smtp.Client, error) {
	dialer := &net.Dialer{Timeout: config.Timeout}
	var connection net.Conn
	var err error
	if config.Security == "tls" {
		connection, err = tls.DialWithDialer(dialer, "tcp", config.endpoint(), config.tlsConfig())
	} else {
		connection, err = dialer.Dial("tcp", config.endpoint())
	}
	if err != nil {
		return nil, fmt.Errorf("SMTP 연결 실패: %w", err)
	}
	// The whole conversation, not only the dial, has to finish in time.
	_ = connection.SetDeadline(time.Now().Add(config.Timeout))
	client, err := smtp.NewClient(connection, config.Host)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("SMTP 세션 시작 실패: %w", err)
	}
	return client, nil
}

// startSession upgrades and authenticates only as far as the relay allows, so
// an unauthenticated internal relay works with the same settings as a hosted
// provider that demands both.
func startSession(client *smtp.Client, config Config) error {
	if err := client.Hello(helloName(config)); err != nil {
		return fmt.Errorf("EHLO 실패: %w", err)
	}
	if config.Security == "starttls" || config.Security == "auto" {
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err := client.StartTLS(config.tlsConfig()); err != nil {
				return fmt.Errorf("STARTTLS 실패: %w", err)
			}
		} else if config.Security == "starttls" {
			return fmt.Errorf("%w: 서버가 STARTTLS 를 지원하지 않습니다", ErrInvalid)
		}
	}
	if strings.TrimSpace(config.Username) == "" {
		return nil
	}
	supported, mechanisms := client.Extension("AUTH")
	if !supported {
		return fmt.Errorf("%w: 서버가 인증을 지원하지 않습니다. mail.username 을 비우고 사용하세요", ErrInvalid)
	}
	var err error
	switch {
	case strings.Contains(strings.ToUpper(mechanisms), "PLAIN"):
		err = client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.Host))
	case strings.Contains(strings.ToUpper(mechanisms), "LOGIN"):
		err = client.Auth(loginAuth{username: config.Username, password: config.Password, host: config.Host})
	default:
		err = client.Auth(smtp.CRAMMD5Auth(config.Username, config.Password))
	}
	if err != nil {
		return fmt.Errorf("SMTP 인증 실패: %w", err)
	}
	return nil
}

func (c Config) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.SkipVerify} //nolint:gosec // opt-in for internal relays with private certificates
}

// helloName keeps the EHLO name to the sender domain, which relays that check
// the greeting are happier with than a container hostname.
func helloName(config Config) string {
	if index := strings.LastIndex(config.FromAddress, "@"); index >= 0 && index+1 < len(config.FromAddress) {
		return config.FromAddress[index+1:]
	}
	return "localhost"
}

// loginAuth implements the LOGIN mechanism that several corporate relays use
// instead of PLAIN. The standard library only ships PLAIN and CRAM-MD5.
type loginAuth struct{ username, password, host string }

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && server.Name != a.host {
		return "", nil, errors.New("LOGIN 인증은 신뢰할 수 있는 서버에서만 사용합니다")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimRight(string(fromServer), ": ")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("알 수 없는 LOGIN 요청: %s", fromServer)
}
