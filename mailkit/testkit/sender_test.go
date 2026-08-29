// SPDX-License-Identifier: Apache-2.0

package testkit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gopherium/framework/mailkit"
	"github.com/gopherium/framework/mailkit/testkit"
)

func TestSenderKeepsWhatItWasGiven(t *testing.T) {
	t.Parallel()

	sender := &testkit.Sender{}

	if err := sender.Send(t.Context(), mailkit.Message{
		To:      "maria@example.com",
		Subject: "You are invited",
		Body:    "Set your password.",
	}); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	if len(sender.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(sender.Messages))
	}
	held := sender.Messages[0]
	if held.To != "maria@example.com" || held.Subject != "You are invited" || held.Body != "Set your password." {
		t.Errorf("message = %+v, want the one that was sent", held)
	}
}

func TestSenderKeepsEveryMessageInOrder(t *testing.T) {
	t.Parallel()

	sender := &testkit.Sender{}

	for _, to := range []string{"maria@example.com", "ada@example.com"} {
		if err := sender.Send(t.Context(), mailkit.Message{To: to, Subject: "S", Body: "B"}); err != nil {
			t.Fatalf("Send(%s) error = %v, want nil", to, err)
		}
	}

	if len(sender.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(sender.Messages))
	}
	if sender.Messages[0].To != "maria@example.com" || sender.Messages[1].To != "ada@example.com" {
		t.Errorf("messages = %+v, want them kept in sending order", sender.Messages)
	}
}

func TestSenderRefusesAnEmptyRecipient(t *testing.T) {
	t.Parallel()

	sender := &testkit.Sender{}

	err := sender.Send(t.Context(), mailkit.Message{Subject: "S", Body: "B"})

	if !errors.Is(err, mailkit.ErrNoRecipient) {
		t.Errorf("Send() error = %v, want mailkit.ErrNoRecipient", err)
	}
	if len(sender.Messages) != 0 {
		t.Errorf("messages = %d, want the refused message kept out", len(sender.Messages))
	}
}

func TestSenderRefusesAMalformedRecipient(t *testing.T) {
	t.Parallel()

	sender := &testkit.Sender{}

	err := sender.Send(t.Context(), mailkit.Message{To: "not-an-address", Subject: "S", Body: "B"})

	if !errors.Is(err, mailkit.ErrInvalidRecipient) {
		t.Errorf("Send() error = %v, want mailkit.ErrInvalidRecipient", err)
	}
	if len(sender.Messages) != 0 {
		t.Errorf("messages = %d, want the refused message kept out", len(sender.Messages))
	}
}

func TestSenderAnswersTheConfiguredFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("relay down")
	sender := &testkit.Sender{Err: boom}

	err := sender.Send(t.Context(), mailkit.Message{To: "maria@example.com", Subject: "S", Body: "B"})

	if !errors.Is(err, boom) {
		t.Errorf("Send() error = %v, want the configured failure", err)
	}
	if len(sender.Messages) != 0 {
		t.Errorf("messages = %d, want a failed send kept out", len(sender.Messages))
	}
}

func TestSenderRefusesTheRecipientBeforeTheConfiguredFailure(t *testing.T) {
	t.Parallel()

	sender := &testkit.Sender{Err: errors.New("relay down")}

	err := sender.Send(t.Context(), mailkit.Message{Subject: "S", Body: "B"})

	if !errors.Is(err, mailkit.ErrNoRecipient) {
		t.Errorf("Send() error = %v, want the recipient refused first, as the real sender does", err)
	}
}

func TestSenderHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	sender := &testkit.Sender{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := sender.Send(ctx, mailkit.Message{To: "maria@example.com", Subject: "S", Body: "B"})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Send() error = %v, want context.Canceled, as a delivering sender answers", err)
	}
	if len(sender.Messages) != 0 {
		t.Errorf("messages = %d, want a cancelled send kept out", len(sender.Messages))
	}
}

func TestSenderAcceptsTheAddressFormsADeliveringSenderAccepts(t *testing.T) {
	t.Parallel()

	sender := &testkit.Sender{}

	for _, to := range []string{"maria@example.com", "Maria Perez <maria@example.com>", "<ada@example.com>"} {
		if err := sender.Send(t.Context(), mailkit.Message{To: to, Subject: "S", Body: "B"}); err != nil {
			t.Errorf("Send(%q) error = %v, want the address accepted", to, err)
		}
	}
	if len(sender.Messages) != 3 {
		t.Errorf("messages = %d, want all three kept", len(sender.Messages))
	}
}

func TestSenderRefusesTheAddressFormsADeliveringSenderRefuses(t *testing.T) {
	t.Parallel()

	refused := map[string]string{
		"several addresses": "maria@example.com, ada@example.com",
		"no domain":         "maria@",
		"no local part":     "@example.com",
		"only spaces":       "   ",
		"a bare name":       "Maria Perez",
	}
	for name, to := range refused {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sender := &testkit.Sender{}

			err := sender.Send(t.Context(), mailkit.Message{To: to, Subject: "S", Body: "B"})

			if !errors.Is(err, mailkit.ErrInvalidRecipient) {
				t.Errorf("Send(%q) error = %v, want mailkit.ErrInvalidRecipient", to, err)
			}
			if len(sender.Messages) != 0 {
				t.Errorf("messages = %d, want the refused message kept out", len(sender.Messages))
			}
		})
	}
}

func TestSenderRefusesAMalformedRecipientBeforeTheConfiguredFailure(t *testing.T) {
	t.Parallel()

	sender := &testkit.Sender{Err: errors.New("relay down")}

	err := sender.Send(t.Context(), mailkit.Message{To: "not-an-address", Subject: "S", Body: "B"})

	if !errors.Is(err, mailkit.ErrInvalidRecipient) {
		t.Errorf("Send() error = %v, want the recipient refused first, as the real sender does", err)
	}
}

func TestSenderSatisfiesTheMailkitSeam(t *testing.T) {
	t.Parallel()

	var _ mailkit.Sender = (*testkit.Sender)(nil)
}
