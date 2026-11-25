# Changelog

All notable changes to GoSPA, the `gospa` module, are documented in
this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `gospa/vX.Y.Z`.

## [Unreleased]

### Added

- `App`, a handler that serves a built single-page app from an `fs.FS`, sending `index.html` for every path that names no file outside `assets/`.
- A path under `assets/` that names no file answers not found.
- A hashed file under `assets/` is kept for `HashedMaxAge`, one year by default, and every other file and the page get `no-cache`.
- `App` fails when the build holds no `index.html`, when `HashedMaxAge` is below one second, and on a value with no name or a name given twice.
- `Meta`, server values the page carries as meta elements where its head opens.
- The page is read again each time it is sent and checked by a tag of the bytes sent.
- `Files`, a handler that sends the regular files of an `fs.FS` and nothing else.
- Both answer only GET and HEAD, never list a folder and never send a name starting with a dot, `.well-known` excepted.
- A path with an empty segment, or a segment that holds a tilde or ends in a dot or a space, answers not found.
- A file with no date, or dated at the Unix epoch, that can seek is checked by a tag of the bytes sent, worked out on every request.
- A file that cannot seek is read only as far as the answer needs, and gets the whole file when asked for several ranges.
- Types come from GoSPA's own table, `application/octet-stream` for an extension it does not know, and every file and page carries `X-Content-Type-Options: nosniff`.
- `AppOptions.NotFound` and `FilesOptions.NotFound` replace the plain not found answer.
- Requires `golang.org/x/net` v0.63.0.
