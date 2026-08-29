// SPDX-License-Identifier: Apache-2.0

package smtp_test

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// authRelay is a relay that upgrades to TLS, offers authentication and keeps what it is answered.
type authRelay struct {
	port     int
	roots    *x509.CertPool
	cert     tls.Certificate
	answered chan string
}

// newAuthRelay starts a relay that offers STARTTLS then authentication, refusing every credential.
func newAuthRelay(t *testing.T) *authRelay {
	t.Helper()
	cert, roots := selfSigned(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	relay := &authRelay{
		port:     listener.Addr().(*net.TCPAddr).Port,
		roots:    roots,
		cert:     cert,
		answered: make(chan string, 1),
	}
	go relay.accept(listener)
	return relay
}

// accept serves sessions until the listener closes.
func (a *authRelay) accept(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go a.serve(conn)
	}
}

// serve walks one session through the upgrade to the credential and refuses it.
func (a *authRelay) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	_, _ = fmt.Fprint(conn, "220 relay\r\n")
	secured := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			if secured {
				_, _ = fmt.Fprint(conn, "250-relay\r\n250 AUTH PLAIN\r\n")
				continue
			}
			_, _ = fmt.Fprint(conn, "250-relay\r\n250 STARTTLS\r\n")
		case strings.HasPrefix(line, "STARTTLS"):
			_, _ = fmt.Fprint(conn, "220 go ahead\r\n")
			secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{a.cert}})
			if err := secure.Handshake(); err != nil {
				return
			}
			conn, reader, secured = secure, bufio.NewReader(secure), true
		case strings.HasPrefix(line, "AUTH PLAIN"):
			a.keep(strings.TrimSpace(strings.TrimPrefix(line, "AUTH PLAIN")))
			_, _ = fmt.Fprint(conn, "535 refused\r\n")
		default:
			_, _ = fmt.Fprint(conn, "250 ok\r\n")
		}
	}
}

// keep decodes one offered credential and records it.
func (a *authRelay) keep(encoded string) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return
	}
	select {
	case a.answered <- strings.ReplaceAll(string(raw), "\x00", " "):
	default:
	}
}

// selfSigned returns a certificate for the loopback address and the roots trusting it.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "relay.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: parsed}, roots
}
