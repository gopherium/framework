# Changelog

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versioning follows [Semantic Versioning](https://semver.org/). While at 0.x,
minor releases may break. Releases are tagged `gottext/vX.Y.Z` and publish
from CI.

## [0.5.0] - 2026-09-01

### Fixed

- The lockfile gate reads the project's packages when the lockfile
  holds more than one document.

## [0.4.0] - 2026-09-01

### Fixed

- A catalogue entry under a "constructor"-named or "__proto__"-named
  context survives every merge instead of vanishing onto the global
  Object, pushing one no longer crashes when the platform lacks the
  context, and the placeholder gate no longer crashes reading a
  template context the catalogue does not answer.
- A flattened export keeps every local plural form in the push, an
  entry only settles when the export answers every form the committed
  catalogue fills, so a locally empty form no longer holds a settled
  locale pending forever.
- The placeholder gate reads printf modifiers, so a translation
  dropping or changing forms such as %02d, %.2f or a named placeholder
  carrying width and precision is caught as mismatched, while an
  escaped percent and ordinary prose carrying a literal percent are
  left alone.
- The locale start is transactional, the display locale commits only
  after every catalogue loads, a superseded start drops its results,
  and every switch replaces each domain's catalogue instead of
  merging stale entries.
- Peer-qualified lockfile keys answer their bare versions, so the
  duplicate resolution gate sees packages resolved with peer suffixes.
- Concurrent platform uploads queue behind each other, delay and
  retry included, so they cannot share one pacing window.

## [0.3.0] - 2026-08-25

### Added

- `pushTranslations`, which carries every held catalogue of a supported language
  to the platform with its fuzzy flags, trimmed to the template so a stale file
  revives nothing, and holding back every answer the platform has settled so a
  push never overwrites a reviewer.
- `addLanguage` on the platform seam, and the push adds and fills a supported
  language the platform does not list yet.
- Upload pacing, one paced retry when the platform answers its rate refusal.
- A fuzzy aware merge, where a reviewed answer settles a fuzzy one and a fuzzy
  export never replaces a settled answer, restored answers keeping their flag.
- `unreviewed`, which names the answers still carrying the fuzzy flag.

### Changed

- Clearing a fuzzy flag alone now counts as a meaningful change, so a pull
  writes an approval home even when the text did not move.

## [0.2.0] - 2026-08-24

### Added

- `errorText`, which shows a refused answer in the reader's own language,
  filling the template its code names from the values the answer carries and
  speaking the server's own words when no template fits. The caller supplies
  the templates and the words to fall back on, so both stay translatable.
- A `./testing` entry with `resetLocale`, which takes every text domain and the
  display locale back to their sources between tests.
- `globCatalogs`, which turns the lazy chunks a bundler globbed into the loader
  a catalogue entry wants, reading the locale from each file's own name.

### Changed

- `startLocale` shows the default locale from the moment it is asked, so
  `displayLocale` and `formatDate` never answer en-US to a consumer whose
  sources are written in another locale.

## [0.1.1] - 2026-08-21

### Added

- `resolvedVersions` and `pinnedVersions`, which read a pnpm lockfile and the
  manifests that pin a package, so a repository can gate itself against
  resolving two copies of anything that holds module state.

### Fixed

- A message sharing its name with an object prototype member, `constructor` and
  `toString` among them, no longer crashes the gates or slips past the orphan
  check.
- `serializeCatalog` answers readable JSON for a catalogue carrying no metadata
  entry, where it used to write the bare word undefined.

## [0.1.0] - 2026-08-20

### Added

- `startLocale`, which resolves a locale, loads every catalogue in parallel and
  sets each under its own text domain before it returns. A catalogue naming no
  domain lands under the default domain the WordPress packages read.
- `displayLocale`, `rememberLocale` and `formatDate`, the seam dates and numbers
  follow. `formatDate` takes a date or the text a server stored, and the options
  bag any real format needs.
- `pot`, which extracts messages through the four gettext call shapes and
  optionally through Go markers and templates. The Go walk reads nothing unless
  it is given roots.
- `compileCatalog` and `serializeCatalog`, turning PO sources into the JSON a
  locale chunk ships, metadata entry first and deterministic.
- `untranslated`, `orphaned` and `mismatched`, the three gates over a
  catalogue's health. `mismatched` refuses a translation whose placeholders
  its message does not name, bare placeholders included.
- `syncTranslations` and `poeditorAt`, which carry translations home from a
  platform. A translation once set is never removed by a sync, and the summary
  says how many answers were kept where the platform holds nothing.
