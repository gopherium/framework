# gottext

The translation brick for Gopherium projects. It carries the gettext
pipeline an application needs to speak more than one language: a
runtime that loads compiled catalogs per text domain, a build layer
that extracts messages into a POT template, compiles PO catalogs to
JSON and gates their health, and a sync layer that exchanges
translations with POEditor without ever removing one.

## Install

```sh
pnpm add @gopherium/gottext @wordpress/i18n
```

The build entry extracts messages from your sources, which needs one
more package as a development dependency:

```sh
pnpm add -D gettext-extractor
```

## License

Apache-2.0. See NOTICE for how the GPL licensed runtime peer is
handled.
