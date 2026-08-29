// SPDX-License-Identifier: Apache-2.0

// Package testkit provides test doubles for mailkit consumers.
package testkit

import (
	"context"
	"fmt"
	"net/mail"

	"github.com/gopherium/framework/mailkit"
)

// Sender keeps the messages it is given rather than delivering them,
// refusing a recipient and a cancelled context as a delivering sender
// does. Err forces a delivery failure. It holds no lock, so a test
// sending from several goroutines synchronizes itself.
type Sender struct {
	Messages []mailkit.Message
	Err      error
}

// Send keeps m, or answers the recipient refusal, the cancelled
// context, or the configured failure.
func (s *Sender) Send(ctx context.Context, m mailkit.Message) error {
	if m.To == "" {
		return mailkit.ErrNoRecipient
	}
	if _, err := mail.ParseAddress(m.To); err != nil {
		return fmt.Errorf("mailkit/testkit: %w %s: %w", mailkit.ErrInvalidRecipient, m.To, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mailkit/testkit: %w", err)
	}
	if s.Err != nil {
		return s.Err
	}
	s.Messages = append(s.Messages, m)
	return nil
}
