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

## Dates and numbers

The root entry writes dates, times, numbers and money in a format
locale that may differ from the interface language. Hand the locale
your settings name to `rememberFormatLocale`, then call `formatDate`,
`formatTime`, `formatNumber` and `formatMoney`. Until a format locale
is remembered they follow the interface locale. `formatWeekday` and
`formatList` always follow the interface language.

The react entry offers `FormatLocaleGate`, which holds your screens
back until your settings name the format locale. It needs React:

```sh
pnpm add react
```

The build entry's `unformatted` gate names every message of a
template that writes a number through `%d`, `%i`, `%u`, `%e`, `%f` or
`%g`, upper case `%E` and `%G` and a length such as `%ld` included, so
each count reaches the reader through `formatNumber` and `%s`.

## License

Apache-2.0. See NOTICE for how the GPL licensed runtime peer is
handled.
