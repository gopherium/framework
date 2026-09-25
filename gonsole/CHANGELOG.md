# Changelog

All notable changes to the `gonsole` module are documented in this
file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `gonsole/vX.Y.Z`.

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

[0.1.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fv0.1.0
