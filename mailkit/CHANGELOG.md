# Changelog

All notable changes to the `mailkit` module are documented in this
file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `mailkit/vX.Y.Z`.

## [Unreleased]

### Added

- `Message` and `Sender`, the one method seam every mail provider satisfies.
- `ErrNoRecipient`, answered for an empty recipient.
- `ErrInvalidRecipient`, answered for an address the transport rejects.
- `ErrNoSubject`, answered for a template that renders nothing.
- `Templates` and `NewTemplates`, rendering mail from template files
  whose first non-blank line is the subject and whose remainder is the
  body, over a default filesystem the caller supplies.
- A named override directory whose files replace the defaults by
  filename, read at render time.
- `smtp.Sender` and `smtp.New`, delivering over SMTP through go-mail.
- `smtp.Config`, naming the relay host, port, credentials, sender
  address, transport security, HELO name, timeout and trust roots.
- Credentials require mandatory transport security, so a relay that
  drops the upgrade never receives them.
- `smtp.TLS` with `TLSMandatory`, `TLSOpportunistic` and `TLSNone`, over
  STARTTLS alone, so a relay speaking implicit TLS on port 465 is not
  reachable and fails as a dial timeout. An empty value applies
  mandatory.
- `smtp.Config.TLSConfig`, naming the roots a relay is verified against,
  so a relay holding a private certificate is reachable. Nil verifies
  against the system roots.
- `smtp.DefaultPort`, the submission port 587 a sender dials when the
  config names none.
- `testkit.Sender` with `Messages` and `Err`, keeping messages rather
  than delivering them and answering the same refusals a delivering
  sender answers.
