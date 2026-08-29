// SPDX-License-Identifier: Apache-2.0

package smtp_test

import (
	"bufio"
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// authRelay is a relay that offers challenge response authentication and keeps what it is answered.
type authRelay struct {
	port     int
	answered chan string
}

// newAuthRelay starts a relay offering CRAM-MD5 and answering every credential with a refusal.
func newAuthRelay(t *testing.T) *authRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	relay := &authRelay{port: listener.Addr().(*net.TCPAddr).Port, answered: make(chan string, 1)}
	go relay.accept(listener)
	return relay
}

// accept serves one session at a time until the listener closes.
func (a *authRelay) accept(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go a.serve(conn)
	}
}

// serve walks one session as far as the credential and then refuses it.
func (a *authRelay) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	_, _ = fmt.Fprint(conn, "220 relay\r\n")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			_, _ = fmt.Fprint(conn, "250-relay\r\n250 AUTH CRAM-MD5\r\n")
		case strings.HasPrefix(line, "AUTH CRAM-MD5"):
			_, _ = fmt.Fprintf(conn, "334 %s\r\n", base64.StdEncoding.EncodeToString([]byte(cramChallenge)))
			answer, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(answer))
			select {
			case a.answered <- string(raw):
			default:
			}
			_, _ = fmt.Fprint(conn, "535 refused\r\n")
		default:
			_, _ = fmt.Fprint(conn, "250 ok\r\n")
		}
	}
}

// cramChallenge is the fixed challenge the relay offers.
const cramChallenge = "<mailkit@example.com>"

// cramAnswer answers what a client holding the credentials must reply to cramChallenge.
func cramAnswer(username, password string) string {
	mac := hmac.New(md5.New, []byte(password))
	mac.Write([]byte(cramChallenge))
	return fmt.Sprintf("%s %x", username, mac.Sum(nil))
}
