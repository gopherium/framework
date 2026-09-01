// SPDX-License-Identifier: Apache-2.0

import { setLocaleData } from '@wordpress/i18n'

import type { Catalog } from './catalog.js'
import { rememberLocale } from './display.js'

/** CatalogEntry names one text domain beside how its catalogue loads. */
export interface CatalogEntry {
	/** domain is the text domain the catalogue answers, the default domain when absent. */
	domain?: string
	/** load answers the catalogue for a locale, or nothing when none ships for it. */
	load: (locale: string) => Promise<Catalog | undefined>
}

/** LocaleOptions carries what a start needs beyond its domains. */
export interface LocaleOptions {
	/** defaultLocale is the sources' own locale, shown while the resolver runs and loading no catalogue of its own. */
	defaultLocale?: string
}

/**
 * Resolves the locale, loads every catalogue in parallel, and sets each under its domain.
 * @param resolve - Answers the locale the interface should stand in.
 * @param entries - The text domains to load.
 * @param options - What the start needs beyond its domains.
 * @returns The settled locale.
 */
export async function startLocale(
	resolve: () => Promise<string>,
	entries: CatalogEntry[],
	options: LocaleOptions = {},
): Promise<string> {
	if (options.defaultLocale !== undefined) {
		rememberLocale(options.defaultLocale)
	}
	const locale = await resolve()
	rememberLocale(locale)
	if (locale === options.defaultLocale) {
		return locale
	}
	const loaded = await Promise.all(entries.map((entry) => entry.load(locale)))
	entries.forEach((entry, at) => {
		const catalog = loaded[at]
		if (catalog !== undefined) {
			setLocaleData(catalog, entry.domain)
		}
	})
	return locale
}
