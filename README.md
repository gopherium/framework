# Gopherium Framework

**The gopherium brick shelf.** Self contained building bricks for Go
applications, one separately versioned module per brick. Each brick is
small, framework-free, and usable on its own. You pin the modules you
need and ignore the rest.

> **Stability: v0.** Module APIs may change between minor releases
> while they mature. Pin a version and read the module's CHANGELOG
> before upgrading. Production use is at your own risk until v1.

## Modules

- [`mailkit`](mailkit/) renders mail from template files and sends it
  over SMTP.

## Design

One repository, one Go module per brick, no shared code between them.
Every module carries its own go.mod, its own CHANGELOG and its own
lint configuration, and is released independently under a path
prefixed tag such as `mailkit/v0.1.0`. Modules depend on published
versions only, never on sibling source, so what you pin is what you
get.

## Reporting security issues

See [SECURITY.md](SECURITY.md). Do not open public issues for
vulnerabilities.

## License

Apache-2.0. Copyright © 2026 Manuel 'SirLouen' Camargo.
