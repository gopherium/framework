// SPDX-License-Identifier: Apache-2.0

// Package smtp delivers mailkit messages over SMTP.
package smtp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/gopherium/framework/mailkit"
)

// TLS names the transport security a relay is held to. Mandatory and
// opportunistic both mean STARTTLS, so a relay speaking implicit TLS on
// port 465 is not reachable and fails as a dial timeout.
type TLS string

// The transport security policies a sender accepts.
const (
	TLSMandatory     TLS = "mandatory"
	TLSOpportunistic TLS = "opportunistic"
	TLSNone          TLS = "none"
)

// DefaultPort is the submission port a sender dials when the config names none.
const DefaultPort = 587

// Config names the relay a sender delivers through.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLS      TLS
	HELO     string
	Timeout  time.Duration
}

// Sender delivers mailkit messages through one relay. It is safe to
// share across goroutines, and it dials once per message. Cancelling
// the context governs the dial alone, so a transmission already under
// way is not aborted and its mail may still arrive.
type Sender struct {
	client *gomail.Client
	from   *mail.Address
}

// dialedKey carries the connection holder one send hands to the dialer.
type dialedKey struct{}

// dialed remembers the connection a dial opened so a failure can close it.
type dialed struct{ conn net.Conn }

// New returns a Sender for the relay cfg names, refusing an unusable config.
func New(cfg Config) (*Sender, error) {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("mailkit/smtp: from address: %w", err)
	}
	opts, err := options(cfg)
	if err != nil {
		return nil, err
	}
	client, err := gomail.NewClient(cfg.Host, append(opts, gomail.WithDialContextFunc(dial))...)
	if err != nil {
		return nil, fmt.Errorf("mailkit/smtp: build client: %w", err)
	}
	return &Sender{client: client, from: from}, nil
}

// dial opens the connection and hands it to the holder the sending context carries.
func dial(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if held, ok := ctx.Value(dialedKey{}).(*dialed); ok && err == nil {
		held.conn = conn
	}
	return conn, err
}

// Send delivers m through the relay, refusing an empty or malformed recipient.
func (s *Sender) Send(ctx context.Context, m mailkit.Message) error {
	if m.To == "" {
		return mailkit.ErrNoRecipient
	}
	msg := gomail.NewMsg()
	msg.FromMailAddress(s.from)
	if err := msg.To(m.To); err != nil {
		return fmt.Errorf("mailkit/smtp: %w %s: %w", mailkit.ErrInvalidRecipient, m.To, err)
	}
	msg.Subject(m.Subject)
	msg.SetBodyString(gomail.TypeTextPlain, m.Body)
	return s.deliver(ctx, m.To, msg)
}

// deliver sends one message, closing the connection a failed dial leaves open.
func (s *Sender) deliver(ctx context.Context, to string, msg *gomail.Msg) error {
	held := &dialed{}
	if err := s.client.DialAndSendWithContext(context.WithValue(ctx, dialedKey{}, held), msg); err != nil {
		if held.conn != nil {
			_ = held.conn.Close()
		}
		return fmt.Errorf("mailkit/smtp: deliver to %s: %w", to, err)
	}
	return nil
}

// options answers the go-mail client options cfg asks for, refusing an unusable one.
func options(cfg Config) ([]gomail.Option, error) {
	if cfg.Host == "" {
		return nil, errors.New("mailkit/smtp: no host")
	}
	policy, err := policyOf(cfg.TLS)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout < 0 {
		return nil, errors.New("mailkit/smtp: negative timeout")
	}
	auth, err := credentials(cfg)
	if err != nil {
		return nil, err
	}
	opts := []gomail.Option{gomail.WithPort(port(cfg.Port)), gomail.WithTLSPolicy(policy)}
	if cfg.Timeout > 0 {
		opts = append(opts, gomail.WithTimeout(cfg.Timeout))
	}
	if cfg.HELO != "" {
		opts = append(opts, gomail.WithHELO(cfg.HELO))
	}
	return append(opts, auth...), nil
}

// credentials answers the authentication options cfg asks for, refusing an unusable pair.
func credentials(cfg Config) ([]gomail.Option, error) {
	if cfg.Username == "" && cfg.Password == "" {
		return nil, nil
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("mailkit/smtp: username and password go together")
	}
	if cfg.TLS == TLSNone {
		return nil, errors.New("mailkit/smtp: credentials need transport security")
	}
	return []gomail.Option{
		gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
		gomail.WithUsername(cfg.Username),
		gomail.WithPassword(cfg.Password),
	}, nil
}

// policyOf maps a named policy onto go-mail, treating the empty one as mandatory.
func policyOf(named TLS) (gomail.TLSPolicy, error) {
	switch named {
	case TLSMandatory, "":
		return gomail.TLSMandatory, nil
	case TLSOpportunistic:
		return gomail.TLSOpportunistic, nil
	case TLSNone:
		return gomail.NoTLS, nil
	default:
		return 0, fmt.Errorf("mailkit/smtp: unknown tls policy %q", named)
	}
}

// port answers the configured port, or the submission default when none is named.
func port(named int) int {
	if named == 0 {
		return DefaultPort
	}
	return named
}
