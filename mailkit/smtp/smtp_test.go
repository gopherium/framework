// SPDX-License-Identifier: Apache-2.0

package smtp_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	smtpmock "github.com/mocktools/go-smtp-mock/v2"

	"github.com/gopherium/framework/mailkit"
	"github.com/gopherium/framework/mailkit/smtp"
)

// relay starts a mock SMTP server that survives the reset go-mail sends after delivering.
func relay(t *testing.T) *smtpmock.Server {
	t.Helper()
	server := smtpmock.New(smtpmock.ConfigurationAttr{MultipleMessageReceiving: true})
	if err := server.Start(); err != nil {
		t.Fatalf("starting the mock relay: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	return server
}

// senderTo builds a sender aimed at the mock, which speaks neither TLS nor authentication.
func senderTo(t *testing.T, server *smtpmock.Server) *smtp.Sender {
	t.Helper()
	held, err := smtp.New(smtp.Config{
		Host: "127.0.0.1",
		Port: server.PortNumber(),
		From: "crm@example.com",
		TLS:  smtp.TLSNone,
		HELO: "example.com",
	})
	if err != nil {
		t.Fatalf("smtp.New() error = %v, want nil", err)
	}
	return held
}

// delivered answers the payload of the one message the relay received.
func delivered(t *testing.T, server *smtpmock.Server) string {
	t.Helper()
	messages, err := server.WaitForMessages(1, 5*time.Second)
	if err != nil {
		t.Fatalf("waiting for the message: %v", err)
	}
	return messages[0].MsgRequest()
}

func TestSendDeliversSubjectBodyAndRecipient(t *testing.T) {
	t.Parallel()

	server := relay(t)
	sender := senderTo(t, server)

	err := sender.Send(t.Context(), mailkit.Message{
		To:      "maria@example.com",
		Subject: "You are invited",
		Body:    "Set your password to begin.",
	})

	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	payload := delivered(t, server)
	wants := []string{"You are invited", "Set your password to begin.", "maria@example.com", "crm@example.com"}
	for _, want := range wants {
		if !strings.Contains(payload, want) {
			t.Errorf("payload missing %q, got:\n%s", want, payload)
		}
	}
}

func TestSendAddressesTheEnvelopeToTheRecipient(t *testing.T) {
	t.Parallel()

	server := relay(t)
	sender := senderTo(t, server)

	if err := sender.Send(t.Context(), mailkit.Message{
		To:      "maria@example.com",
		Subject: "Subject",
		Body:    "Body",
	}); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	messages, err := server.WaitForMessages(1, 5*time.Second)
	if err != nil {
		t.Fatalf("waiting for the message: %v", err)
	}
	if from := messages[0].MailfromRequest(); !strings.Contains(from, "crm@example.com") {
		t.Errorf("envelope sender = %q, want the configured from", from)
	}
	pairs := messages[0].RcpttoRequestResponse()
	if len(pairs) != 1 || !strings.Contains(pairs[0][0], "maria@example.com") {
		t.Errorf("envelope recipients = %v, want the one recipient", pairs)
	}
}

func TestSendCarriesAPlainTextBody(t *testing.T) {
	t.Parallel()

	server := relay(t)
	sender := senderTo(t, server)

	if err := sender.Send(t.Context(), mailkit.Message{
		To:      "maria@example.com",
		Subject: "Subject",
		Body:    "Body",
	}); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	payload := delivered(t, server)
	if !strings.Contains(payload, "text/plain") {
		t.Errorf("payload is not plain text, got:\n%s", payload)
	}
	for _, unwanted := range []string{"text/html", "multipart/"} {
		if strings.Contains(payload, unwanted) {
			t.Errorf("payload carries %q, want text only, got:\n%s", unwanted, payload)
		}
	}
}

func TestSendRefusesAnEmptyRecipientBeforeDialing(t *testing.T) {
	t.Parallel()

	sender, err := smtp.New(smtp.Config{
		Host:    "127.0.0.1",
		Port:    9,
		From:    "crm@example.com",
		TLS:     smtp.TLSNone,
		HELO:    "example.com",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("smtp.New() error = %v, want nil", err)
	}

	sendErr := sender.Send(t.Context(), mailkit.Message{Subject: "Subject", Body: "Body"})

	if !errors.Is(sendErr, mailkit.ErrNoRecipient) {
		t.Errorf("Send() error = %v, want mailkit.ErrNoRecipient with no dial attempted", sendErr)
	}
}

func TestSendRefusesAMalformedRecipient(t *testing.T) {
	t.Parallel()

	server := relay(t)
	sender := senderTo(t, server)

	err := sender.Send(t.Context(), mailkit.Message{To: "not-an-address", Subject: "Subject", Body: "Body"})

	if !errors.Is(err, mailkit.ErrInvalidRecipient) {
		t.Errorf("Send() error = %v, want mailkit.ErrInvalidRecipient", err)
	}
}

func TestSendReportsAnUnreachableRelay(t *testing.T) {
	t.Parallel()

	sender, err := smtp.New(smtp.Config{
		Host:    "127.0.0.1",
		Port:    9,
		From:    "crm@example.com",
		TLS:     smtp.TLSNone,
		HELO:    "example.com",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("smtp.New() error = %v, want nil", err)
	}

	sendErr := sender.Send(t.Context(), mailkit.Message{To: "maria@example.com", Subject: "S", Body: "B"})

	if sendErr == nil {
		t.Fatal("Send() error = nil, want the unreachable relay surfaced")
	}
	if errors.Is(sendErr, mailkit.ErrNoRecipient) || errors.Is(sendErr, mailkit.ErrInvalidRecipient) {
		t.Errorf("Send() error = %v, want a delivery failure, not a recipient refusal", sendErr)
	}
}

func TestSendDeliversEveryMessageOnOneSender(t *testing.T) {
	t.Parallel()

	server := relay(t)
	sender := senderTo(t, server)

	for _, to := range []string{"maria@example.com", "ada@example.com"} {
		if err := sender.Send(t.Context(), mailkit.Message{To: to, Subject: "Subject", Body: "Body"}); err != nil {
			t.Fatalf("Send(%s) error = %v, want nil", to, err)
		}
	}

	messages, err := server.WaitForMessages(2, 5*time.Second)
	if err != nil {
		t.Fatalf("waiting for both messages: %v", err)
	}
	seen := map[string]bool{}
	for _, held := range messages {
		pairs := held.RcpttoRequestResponse()
		if len(pairs) != 1 {
			t.Fatalf("recipients = %v, want one per message", pairs)
		}
		seen[pairs[0][0]] = true
		if !strings.Contains(held.MsgRequest(), "Body") {
			t.Errorf("payload = %q, want the body carried", held.MsgRequest())
		}
	}
	for _, to := range []string{"maria@example.com", "ada@example.com"} {
		if !hasRecipient(seen, to) {
			t.Errorf("recipients %v, want one addressed to %s", seen, to)
		}
	}
}

// hasRecipient reports whether any recorded envelope names the address.
func hasRecipient(seen map[string]bool, address string) bool {
	for held := range seen {
		if strings.Contains(held, address) {
			return true
		}
	}
	return false
}

func TestSendOffersTheConfiguredCredentials(t *testing.T) {
	t.Parallel()

	server := relay(t)
	sender, err := smtp.New(smtp.Config{
		Host:     "127.0.0.1",
		Port:     server.PortNumber(),
		From:     "crm@example.com",
		TLS:      smtp.TLSOpportunistic,
		HELO:     "example.com",
		Username: "crm",
		Password: "a long enough secret",
	})
	if err != nil {
		t.Fatalf("smtp.New() error = %v, want nil", err)
	}

	sendErr := sender.Send(t.Context(), mailkit.Message{To: "maria@example.com", Subject: "S", Body: "B"})

	if sendErr == nil {
		t.Fatal("Send() error = nil, want the relay refusing the offered authentication")
	}
	if !strings.Contains(sendErr.Error(), "AUTH") {
		t.Errorf("Send() error = %v, want authentication to have been attempted", sendErr)
	}
}

func TestSendCarriesTheConfiguredHELO(t *testing.T) {
	t.Parallel()

	server := relay(t)
	sender := senderTo(t, server)

	if err := sender.Send(t.Context(), mailkit.Message{To: "maria@example.com", Subject: "S", Body: "B"}); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	messages, err := server.WaitForMessages(1, 5*time.Second)
	if err != nil {
		t.Fatalf("waiting for the message: %v", err)
	}
	if helo := messages[0].HeloRequest(); !strings.Contains(helo, "example.com") {
		t.Errorf("helo = %q, want the configured name", helo)
	}
}

func TestSendHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	server := relay(t)
	sender := senderTo(t, server)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := sender.Send(ctx, mailkit.Message{To: "maria@example.com", Subject: "S", Body: "B"})

	if err == nil {
		t.Fatal("Send() error = nil, want the cancelled context to have stopped the dial")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Send() error = %v, want context.Canceled wrapped", err)
	}
}

func TestNewRefusesAnUnusableConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]smtp.Config{
		"no host":          {From: "crm@example.com"},
		"no from":          {Host: "127.0.0.1"},
		"malformed from":   {Host: "127.0.0.1", From: "not-an-address"},
		"unknown tls":      {Host: "127.0.0.1", From: "crm@example.com", TLS: "sometimes"},
		"port below range": {Host: "127.0.0.1", From: "crm@example.com", Port: -1},
		"port above range": {Host: "127.0.0.1", From: "crm@example.com", Port: 70000},
		"negative timeout": {Host: "127.0.0.1", From: "crm@example.com", Timeout: -time.Second},
		"username without password": {
			Host: "127.0.0.1", From: "crm@example.com", Username: "crm",
		},
		"password without username": {
			Host: "127.0.0.1", From: "crm@example.com", Password: "a long enough secret",
		},
		"credentials without transport security": {
			Host: "127.0.0.1", From: "crm@example.com", TLS: smtp.TLSNone,
			Username: "crm", Password: "a long enough secret",
		},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := smtp.New(cfg); err == nil {
				t.Errorf("smtp.New() error = nil, want %s refused at construction", name)
			}
		})
	}
}

func TestNewAcceptsEveryTLSPolicy(t *testing.T) {
	t.Parallel()

	for _, policy := range []smtp.TLS{smtp.TLSMandatory, smtp.TLSOpportunistic, smtp.TLSNone, ""} {
		if _, err := smtp.New(smtp.Config{
			Host: "127.0.0.1",
			From: "crm@example.com",
			TLS:  policy,
		}); err != nil {
			t.Errorf("smtp.New(TLS %q) error = %v, want nil", policy, err)
		}
	}
}

func TestNewAcceptsCredentials(t *testing.T) {
	t.Parallel()

	if _, err := smtp.New(smtp.Config{
		Host:     "127.0.0.1",
		From:     "crm@example.com",
		TLS:      smtp.TLSMandatory,
		Username: "crm",
		Password: "a long enough secret",
	}); err != nil {
		t.Errorf("smtp.New() error = %v, want credentials accepted", err)
	}
}

func TestSenderSatisfiesTheMailkitSeam(t *testing.T) {
	t.Parallel()

	var _ mailkit.Sender = (*smtp.Sender)(nil)
}
