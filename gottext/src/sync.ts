// SPDX-License-Identifier: Apache-2.0

import {
	answeredForms,
	keepingAnswers,
	localeFor,
	meaningfulChange,
	namedByTemplate,
	platformCodeOf,
	translated,
	withPluralRuleOf,
	withoutSettled,
} from './merge.js'
import type { Poeditor } from './platform.js'

export {
	answeredForms,
	keepingAnswers,
	localeFor,
	localeOf,
	meaningfulChange,
	namedByTemplate,
	namedMessages,
	translated,
	withPluralRuleOf,
} from './merge.js'
export { poeditorAt } from './platform.js'
export type { PlatformOptions, Poeditor, Retiring } from './platform.js'

/** Catalogues is where the catalogues a sync reads and writes live. */
export interface Catalogues {
	read: (locale: string) => string | undefined
	write: (locale: string, source: string) => void
}

/** Synced is what one sync did, in words fit for a log. */
export interface Synced {
	moved: string[]
	skipped: string[]
	kept: string[]
}

/** Pushed is what one push did, in words fit for a log. */
export interface Pushed {
	pushed: string[]
	skipped: string[]
	added: string[]
}

/** Matched is one language the platform lists that the site answers in. */
interface Matched {
	named: string
	locale: string
}

/**
 * Returns the platform's languages the site answers in, and the words for those it does not.
 * @param platform - The translation platform to read.
 * @param supported - The languages the site answers in.
 * @returns The matched languages and the skip lines for the rest.
 */
async function matchedLanguages(
	platform: Poeditor,
	supported: string[],
): Promise<{ matched: Matched[], skipped: string[] }> {
	const matched: Matched[] = []
	const skipped: string[] = []
	for (const named of await platform.languages()) {
		const locale = localeFor(named, supported)
		if (locale === undefined) {
			skipped.push(`${named}, which the site does not answer in`)
			continue
		}
		matched.push({ named, locale })
	}
	return { matched, skipped }
}

/**
 * Carries every supported catalogue the platform does not list yet, adding its language first.
 * @param platform - The translation platform to write.
 * @param absent - The supported languages the platform does not list, holding a catalogue.
 * @param held - Where the catalogues live.
 * @param template - The catalogue template naming every message the site shows.
 * @param skipped - Where the languages passed over are recorded.
 * @returns The languages that were added and pushed.
 */
async function pushingAbsent(
	platform: Poeditor,
	absent: string[],
	held: Catalogues,
	template: string,
	skipped: string[],
): Promise<string[]> {
	const added: string[] = []
	for (const locale of absent) {
		const current = held.read(locale)
		if (current === undefined) {
			skipped.push(`${locale}, which the repository holds no catalogue for`)
			continue
		}
		const named = platformCodeOf(locale)
		await platform.addLanguage(named)
		await platform.uploadTranslations(named, namedByTemplate(current, template))
		added.push(locale)
	}
	return added
}

/**
 * Carries every repository catalogue to the platform for the languages it lists.
 * @param platform - The translation platform to write.
 * @param supported - The languages the site answers in.
 * @param held - Where the catalogues live.
 * @param template - The catalogue template naming every message the site shows.
 * @returns The languages that were pushed and the ones passed over.
 */
export async function pushTranslations(
	platform: Poeditor,
	supported: string[],
	held: Catalogues,
	template: string,
): Promise<Pushed> {
	const pushed: string[] = []
	await platform.uploadTerms(template)
	const { matched, skipped } = await matchedLanguages(platform, supported)
	for (const { named, locale } of matched) {
		const current = held.read(locale)
		if (current === undefined) {
			skipped.push(`${named}, which the repository holds no catalogue for`)
			continue
		}
		const unsettled = withoutSettled(
			namedByTemplate(current, template),
			await platform.exportPo(named),
		)
		if (translated(unsettled) === 0) {
			skipped.push(`${named}, which the platform has settled every answer of`)
			continue
		}
		await platform.uploadTranslations(named, unsettled)
		pushed.push(locale)
	}
	const listed = matched.map((held) => held.locale)
	const absent = supported.filter((locale) => !listed.includes(locale))
	const added = await pushingAbsent(platform, absent, held, template, skipped)
	return { pushed: [...pushed, ...added], skipped, added }
}

/**
 * Returns the catalogue a sync writes and how many committed forms it restored.
 * @param current - The catalogue as committed, or nothing when none is committed yet.
 * @param exported - The catalogue the platform exported, trimmed to the template.
 * @param template - The catalogue template naming every message the site shows.
 * @returns The catalogue to write and the count of forms the export had lost.
 */
function merged(
	current: string | undefined,
	exported: string,
	template: string,
): { incoming: string, restored: number } {
	if (current === undefined) {
		return { incoming: exported, restored: 0 }
	}
	const clamped = withPluralRuleOf(current, exported)
	const incoming = keepingAnswers(current, clamped, template)
	return { incoming, restored: answeredForms(incoming) - answeredForms(clamped) }
}

/**
 * Carries every translation the platform holds for a language the site answers in.
 * @param platform - The translation platform to read.
 * @param supported - The languages the site answers in.
 * @param held - Where the catalogues live.
 * @param template - The catalogue template naming every message the site shows.
 * @returns The languages that moved, the ones passed over, and the answers kept.
 */
export async function syncTranslations(
	platform: Poeditor,
	supported: string[],
	held: Catalogues,
	template: string,
): Promise<Synced> {
	const moved: string[] = []
	const kept: string[] = []
	await platform.uploadTerms(template)
	const { matched, skipped } = await matchedLanguages(platform, supported)
	for (const { named, locale } of matched) {
		const exported = namedByTemplate(await platform.exportPo(named), template)
		if (translated(exported) === 0) {
			skipped.push(`${named}, which nobody has translated yet`)
			continue
		}
		const current = held.read(locale)
		const { incoming, restored } = merged(current, exported, template)
		if (restored > 0) {
			kept.push(`${locale}, keeping ${restored} answered where the platform holds nothing`)
		}
		if (meaningfulChange(current, incoming)) {
			held.write(locale, incoming)
			moved.push(locale)
		}
	}
	return { moved, skipped, kept }
}
