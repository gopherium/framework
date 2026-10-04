# Changelog

All notable changes to the `gonsole` module are documented in this
file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `gonsole/vX.Y.Z`.

## [0.5.0] - 2026-10-04

### Added

- `Env.Counts`, reading a setting as whole numbers split by commas, each above the one before.
- `Entries`, a bound on how many numbers a `Counts` setting lists.
- `WholeMilliseconds`, a bound refusing a `Duration` setting that is not a whole number of milliseconds.
- The `locale` package, whose `Tag` reads a setting as a canonical BCP 47 language tag through `golang.org/x/text`.

## [0.4.0] - 2026-10-03

### Added

- `StopHost`, stopping a plugin host within a grace, whether or not the run has ended.
- `BaseCommands`, the names of the commands the engine owns.

### Changed

- `Call.DatabaseURL` no longer fails a call that only describes commands, and answers an empty address when the setting is unset.
- The `Release` of `Hosted` stops the host under a context the end of the run cannot cancel.

## [0.3.0] - 2026-09-30

### Added

- `PluginHost` and `Hosted`, building what registering answers from a plugin host, its release bounded by a stop grace.

## [0.2.0] - 2026-09-26

### Added

- `Timeouts.CancelGrace` and `Timeouts.StopGrace`, read from the `SHUTDOWN_CANCEL_GRACE` and `SHUTDOWN_STOP_GRACE` settings.
- `ErrGraceRanOut`, the cause a request cancelled after the shutdown grace sees, and `ErrStillServing`.

### Changed

- `Serve` cancels the requests still running when the shutdown grace ends and logs a warning with their count.
- `Serve` closes the connections left when the cancel grace ends, with a warning, and returns `ErrStillServing` if a request still runs. A hijacked connection stays open.
- `Serve` wraps the `Handler` and the `BaseContext` of the server it runs, keeping the base's values but owning its cancellation.
- `Serve` refuses a grace, cancel grace or stop grace that is not above zero, before it listens.
- `Serve` no longer returns the deadline error of the server's own shutdown.

### Fixed

- `Serve` calls stop under the stop grace once the requests ended, never with what the shutdown grace left.

## [0.1.0] - 2026-09-25

### Added

- `Program`, `Main` and `Run`, running a command line and answering exit code 0, 1 or 2.
- `Command`, `Call` and `Step`, a command, what one run of it receives, and a named schema step.
- `Misuse` and `ErrMisused`, marking an error the program answers with exit 2.
- Command names alone or as `namespace:command`, a help page for each, and a listing of them all.
- `-yes` dry runs for commands that write and `-json` for commands that answer one document.
- `-as` with `Authorize` and `Record`, checking and recording the account that acts.
- `Call.Flags`, the command's own flags the line set, for the audit record.
- The base commands `help`, `list`, `version`, `check`, `serve`, `migrate` and `seed`.
- `Program.Check`, refusing every naming offence in the program and its plugins.
- `Renamed`, keeping an old two word spelling working with a warning.
- `Env` with `Required`, `Duration`, `Count`, `Flag`, `Within`, `Parse` and `Timeouts`.
- `NewServer` and `Serve`, serving HTTP until a signal ends the run.
- `Program.Plugins`, `Call.Plugins`, `Loaded`, `Provider` and `Walk`, for compiled plugins' commands.
- `testkit`, running programs from tests in process and as built binaries.

[0.5.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fv0.5.0
[0.4.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fv0.4.0
[0.3.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fv0.3.0
[0.2.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fv0.2.0
[0.1.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fv0.1.0
