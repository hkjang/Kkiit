// Package mailtest is a relay that keeps what it receives, so tests can prove
// a message left the application without a real SMTP server.
package mailtest

import (
	"bufio"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
)

// Received is one accepted message, headers and all.
type Received struct {
	From string
	To   []string
	Data string
}

// Server speaks enough SMTP for net/smtp: EHLO, optional AUTH PLAIN, MAIL,
// RCPT, DATA, QUIT. It advertises no STARTTLS, which is what an internal
// relay on port 25 looks like.
type Server struct {
	listener net.Listener
	mu       sync.Mutex
	messages []Received
	// Reject makes every MAIL FROM fail with a 550 so a test can see what a
	// refusing relay produces.
	Reject bool
	// Username and Password, when set, are demanded with AUTH PLAIN.
	Username, Password string
}

// Start listens on a loopback port and serves until the test ends.
func Start(t *testing.T) *Server {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &Server{listener: listener}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

// Host and Port are where the relay listens.
func (s *Server) Host() string {
	host, _, _ := net.SplitHostPort(s.listener.Addr().String())
	return host
}
func (s *Server) Port() int { return s.listener.Addr().(*net.TCPAddr).Port }

// Close stops the relay; connections after this are refused, which is what a
// relay that is down looks like.
func (s *Server) Close() { _ = s.listener.Close() }

// Messages returns everything accepted so far.
func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.messages...)
}

func (s *Server) serve() {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(connection)
	}
}

func (s *Server) handle(connection net.Conn) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = connection.Write([]byte(line + "\r\n")) }
	write("220 mailtest ready")
	var current Received
	authenticated := s.Username == ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		command := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			write("250-mailtest")
			if s.Username != "" {
				write("250-AUTH PLAIN")
			}
			write("250 8BITMIME")
		case strings.HasPrefix(command, "AUTH PLAIN"):
			// net/smtp sends the initial response inline: AUTH PLAIN <base64>.
			expected := plain(s.Username, s.Password)
			if strings.TrimSpace(strings.TrimPrefix(line, line[:10])) == expected {
				authenticated = true
				write("235 ok")
			} else {
				write("535 authentication failed")
			}
		case strings.HasPrefix(command, "MAIL FROM:"):
			switch {
			case s.Reject:
				write("550 relay refused")
			case !authenticated:
				write("530 authentication required")
			default:
				// net/smtp appends BODY=8BITMIME after the address.
				current = Received{From: strings.Trim(strings.Fields(line[10:])[0], "<> ")}
				write("250 ok")
			}
		case strings.HasPrefix(command, "RCPT TO:"):
			current.To = append(current.To, strings.Trim(line[8:], "<> "))
			write("250 ok")
		case command == "DATA":
			write("354 end with .")
			var body strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" {
					break
				}
				// Undo dot stuffing the way a real relay does.
				body.WriteString(strings.TrimPrefix(line, "."))
			}
			current.Data = body.String()
			s.mu.Lock()
			s.messages = append(s.messages, current)
			s.mu.Unlock()
			current = Received{}
			write("250 queued")
		case command == "QUIT":
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

func plain(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
}
