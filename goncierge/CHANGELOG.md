# Changelog

Releases of this module are tagged `goncierge/vX.Y.Z`.

## Unreleased

- First release: a `Registry` of the capabilities each role carries, granted, revoked and withdrawn per source.
- `Replace` swaps every grant of one source in one step.
- `Declare` refuses a new capability outside the source's prefix and gives the `Rules.Admin` role every capability the source declares.
- `Declare` refuses a source outside the name rule or holding a dot, and an admin role no other source created.
- Role and capability names are lowercase ASCII letters, digits, `_`, `.` and `-`, start with a letter and hold at most 64 characters.
- Out of scope by design: role inheritance, several roles per account, role definitions per tenant, rule text and storage.
