package mail

import (
	"fmt"
	"mime"
	"strings"
	"time"
)

// Item is one queued notification before it is folded into a message. Subject
// and body come from the same templates the inbox uses, so what a person reads
// in mail and in the bell menu is the same sentence.
type Item struct {
	Subject string
	Body    string
	Link    string
}

// Digest folds everything queued for one recipient into a single message. One
// action often produces several events — accepting a quote creates an order
// and pays it — and three mails for one click is how people end up with a
// rule that drops all of them.
func Digest(config Config, to string, items []Item) Message {
	prefix := "[" + strings.TrimSpace(config.FromName) + "] "
	if strings.TrimSpace(config.FromName) == "" {
		prefix = "[Kkiit] "
	}
	message := Message{To: to}
	if len(items) == 0 {
		return message
	}
	message.Subject = prefix + items[0].Subject
	if len(items) > 1 {
		message.Subject = fmt.Sprintf("%s알림 %d건: %s 외 %d건", prefix, len(items), items[0].Subject, len(items)-1)
	}
	lines := make([]string, 0, len(items)*5+4)
	for index, item := range items {
		if index > 0 {
			lines = append(lines, "", "—", "")
		}
		if len(items) > 1 {
			lines = append(lines, "■ "+item.Subject)
		}
		lines = append(lines, item.Body)
		if link := config.Link(item.Link); link != "" {
			lines = append(lines, "바로 열기: "+link)
		}
	}
	lines = append(lines, "", "—", "이 메일은 알림 설정에 따라 자동으로 발송되었습니다. 받지 않으려면 개인화 > 알림 설정에서 해당 이벤트를 끄세요.")
	if settings := config.Link("/profile/notifications"); settings != "" {
		lines = append(lines, settings)
	}
	message.Body = strings.Join(lines, "\n")
	return message
}

// TestMessage proves the relay works from the console.
func TestMessage(config Config, to string) Message {
	return Digest(config, to, []Item{{
		Subject: "SMTP 발송 테스트",
		Body:    "관리 화면에서 보낸 테스트 메일입니다.\n이 메일을 받았다면 SMTP 설정이 정상입니다.",
	}})
}

// compose builds a MIME message. Korean subjects and names are encoded so
// relays and clients that predate UTF-8 headers still show them correctly.
func compose(config Config, message Message) string {
	var builder strings.Builder
	builder.WriteString("From: " + encodeAddress(config.Address()) + "\r\n")
	builder.WriteString("To: " + strings.TrimSpace(message.To) + "\r\n")
	builder.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", message.Subject) + "\r\n")
	builder.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	builder.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	builder.WriteString("Auto-Submitted: auto-generated\r\n")
	builder.WriteString("X-Kkiit-Notification: 1\r\n")
	builder.WriteString("\r\n")
	builder.WriteString(normalizeBody(message.Body))
	return builder.String()
}

func encodeAddress(address string) string {
	open := strings.LastIndex(address, "<")
	if open <= 0 {
		return address
	}
	return mime.QEncoding.Encode("utf-8", strings.TrimSpace(address[:open])) + " " + address[open:]
}

// normalizeBody uses CRLF line endings. Dot stuffing is left to the DATA
// writer of net/smtp, which already doubles a leading dot; doing it here as
// well would deliver two dots for every line that starts with one.
func normalizeBody(body string) string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if !strings.HasSuffix(body, "\r\n") {
		body += "\r\n"
	}
	return body
}
