// SPDX-License-Identifier: Apache-2.0

package mailkit

import (
	"context"
	"errors"
)

// Message is one mail to one recipient.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers one message.
type Sender interface {
	// Send delivers m, refusing an empty To with ErrNoRecipient.
	Send(ctx context.Context, m Message) error
}

// ErrNoRecipient reports a message whose To is empty.
var ErrNoRecipient = errors.New("mailkit: no recipient")

// ErrInvalidRecipient reports an address the transport refuses.
var ErrInvalidRecipient = errors.New("mailkit: invalid recipient")

// ErrNoSubject reports a template whose rendered output is empty.
var ErrNoSubject = errors.New("mailkit: no subject")
