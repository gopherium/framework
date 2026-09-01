// SPDX-License-Identifier: Apache-2.0

import { po } from 'gettext-parser'
import type { GetTextTranslation, GetTextTranslations } from 'gettext-parser'

import { METADATA, held as ownEntry } from './catalog.js'

/** COMPILED is how every catalogue this brick writes is laid out. */
const COMPILED = { foldLength: 0, eol: '\n' } as const

/**
 * Returns the language a platform code names, in the casing a catalogue file uses.
 * @param code - The code as the platform writes it.
 * @returns The language, with its region upper cased.
 */
export function localeOf(code: string): string {
	const [language, region] = code.split('-')
	return region === undefined ? language : `${language}-${region.toUpperCase()}`
}

/**
 * Returns the code the platform names a language by, bare when its region repeats it.
 * @param locale - The language as a catalogue file names it.
 * @returns The code as the platform writes it.
 */
export function platformCodeOf(locale: string): string {
	const [language, region] = locale.split('-')
	if (region === undefined || region.toLowerCase() === language) {
		return language
	}
	return `${language}-${region.toLowerCase()}`
}

/**
 * Returns the language the site knows a platform code as, if it knows one.
 * @param code - The language as the platform names it.
 * @param supported - The languages the site answers in.
 * @returns The site's own name for it, or nothing when it answers in no such language.
 */
export function localeFor(code: string, supported: string[]): string | undefined {
	if (supported.includes(code)) {
		return code
	}
	if (code.includes('-')) {
		return undefined
	}
	const doubled = localeOf(`${code}-${code}`)
	return supported.includes(doubled) ? doubled : undefined
}

/**
 * Returns how many messages a catalogue answers.
 * @param source - The catalogue as PO text.
 * @returns The count of messages carrying a translation.
 */
export function translated(source: string): number {
	let held = 0
	for (const entries of Object.values(po.parse(source).translations)) {
		for (const [msgid, entry] of Object.entries(entries)) {
			if (msgid !== METADATA && entry.msgstr.some((form) => form !== '')) {
				held += 1
			}
		}
	}
	return held
}

/**
 * Returns how many translated forms a catalogue carries.
 * @param source - The catalogue as PO text.
 * @returns The count of answered forms across every message.
 */
export function answeredForms(source: string): number {
	let held = 0
	for (const entries of Object.values(po.parse(source).translations)) {
		for (const [msgid, entry] of Object.entries(entries)) {
			if (msgid !== METADATA) {
				held += entry.msgstr.filter((form) => form !== '').length
			}
		}
	}
	return held
}

/**
 * Returns an incoming catalogue under the plural rule the committed one declares.
 * @param current - The catalogue as committed.
 * @param incoming - The catalogue the platform exported.
 * @returns The incoming catalogue, carrying the committed plural rule and no form beyond it.
 */
export function withPluralRuleOf(current: string, incoming: string): string {
	const rule = po.parse(current).headers['Plural-Forms']
	if (rule === undefined) {
		return incoming
	}
	const held = po.parse(incoming)
	held.headers['Plural-Forms'] = rule
	const counted = /nplurals\s*=\s*(\d+)/.exec(rule)
	const forms = counted === null ? 0 : Number(counted[1])
	if (forms < 1) {
		throw new Error('the committed catalogue declares a plural rule naming no usable count')
	}
	for (const entries of Object.values(held.translations)) {
		for (const entry of Object.values(entries)) {
			if (entry.msgstr.length > forms) {
				entry.msgstr = entry.msgstr.slice(0, forms)
			}
		}
	}
	return po.compile(held, COMPILED).toString()
}

/**
 * Returns an incoming catalogue without any message the template does not name.
 * @param incoming - The catalogue the platform exported.
 * @param template - The template naming every message the catalogue may carry.
 * @returns The incoming catalogue, trimmed to the template.
 */
export function namedByTemplate(incoming: string, template: string): string {
	const named = po.parse(template).translations
	const held = po.parse(incoming)
	for (const [context, entries] of Object.entries(held.translations)) {
		for (const msgid of Object.keys(entries)) {
			if (msgid !== METADATA && ownEntry(ownEntry(named, context), msgid) === undefined) {
				delete held.translations[context][msgid]
			}
		}
	}
	return po.compile(held, COMPILED).toString()
}

/**
 * Reports whether an entry carries the fuzzy flag.
 * @param entry - The entry as parsed.
 * @returns Whether the entry is fuzzy.
 */
export function fuzzyOf(entry: GetTextTranslation): boolean {
	return /(^|,)\s*fuzzy\s*(,|$)/.test(entry.comments?.flag ?? '')
}

/**
 * Writes whether an entry is fuzzy, leaving its other flags standing.
 * @param entry - The entry to mark.
 * @param fuzzy - Whether the entry is fuzzy.
 */
function flaggedFuzzy(entry: GetTextTranslation, fuzzy: boolean): void {
	const others = (entry.comments?.flag ?? '')
		.split(',')
		.map((flag) => flag.trim())
		.filter((flag) => flag !== '' && flag !== 'fuzzy')
	const flags = fuzzy ? [...others, 'fuzzy'] : others
	if (flags.length > 0) {
		entry.comments = { ...entry.comments, flag: flags.join(', ') }
		return
	}
	if (entry.comments !== undefined) {
		delete entry.comments.flag
	}
}

/**
 * Returns the forms a merge keeps and which sides answered them.
 * @param preferred - The forms that win wherever they are answered.
 * @param fallback - The forms filling what the preferred side holds empty.
 * @returns The forms and whether each side supplied any of them.
 */
function mergedForms(
	preferred: string[],
	fallback: string[],
): { forms: string[], tookPreferred: boolean, tookFallback: boolean } {
	const forms: string[] = []
	let tookPreferred = false
	let tookFallback = false
	for (let at = 0; at < Math.max(preferred.length, fallback.length); at += 1) {
		const wanted = preferred[at] ?? ''
		if (wanted !== '') {
			forms.push(wanted)
			tookPreferred = true
			continue
		}
		forms.push(fallback[at] ?? '')
		tookFallback = tookFallback || (fallback[at] ?? '') !== ''
	}
	return { forms, tookPreferred, tookFallback }
}

/**
 * Writes one committed answer into a catalogue that arrived without every settled form of it.
 * @param held - The catalogue the platform exported, as parsed.
 * @param context - The context the answer sits under.
 * @param msgid - The message the answer belongs to.
 * @param entry - The committed answer.
 */
function restoring(
	held: GetTextTranslations,
	context: string,
	msgid: string,
	entry: GetTextTranslation,
): void {
	const entries: Record<string, GetTextTranslation> = ownEntry(held.translations, context) ?? {}
	held.translations[context] = entries
	const arrived = ownEntry(entries, msgid)
	if (arrived === undefined) {
		entries[msgid] = entry
		return
	}
	arrived.msgid_plural ??= entry.msgid_plural
	const settled = settledForms(entry, arrived)
	arrived.msgstr = settled.forms
	flaggedFuzzy(arrived, settled.fuzzy)
}

/**
 * Returns the merged forms of one answered message and whether they stay fuzzy.
 * @param entry - The committed answer.
 * @param arrived - The answer the platform exported.
 * @returns The forms to keep and the fuzzy state they carry.
 */
function settledForms(
	entry: GetTextTranslation,
	arrived: GetTextTranslation,
): { forms: string[], fuzzy: boolean } {
	const ourFuzzy = fuzzyOf(entry)
	const theirFuzzy = fuzzyOf(arrived)
	const oursFirst = theirFuzzy && !ourFuzzy
	const preferred = oursFirst ? entry.msgstr : arrived.msgstr
	const fallback = oursFirst ? arrived.msgstr : entry.msgstr
	const merged = mergedForms(preferred, fallback)
	const preferredFuzzy = oursFirst ? ourFuzzy : theirFuzzy
	const fallbackFuzzy = oursFirst ? theirFuzzy : ourFuzzy
	return {
		forms: merged.forms,
		fuzzy: (merged.tookPreferred && preferredFuzzy) || (merged.tookFallback && fallbackFuzzy),
	}
}

/**
 * Returns an incoming catalogue restoring every committed answer it lost.
 * @param current - The catalogue as committed.
 * @param incoming - The catalogue the platform exported.
 * @param template - The template naming every message the catalogue may carry.
 * @returns The incoming catalogue, keeping each committed form the export holds empty or lacks.
 */
export function keepingAnswers(current: string, incoming: string, template: string): string {
	const named = po.parse(template).translations
	const ours = po.parse(current).translations
	const held = po.parse(incoming)
	for (const [context, entries] of Object.entries(ours)) {
		for (const [msgid, entry] of Object.entries(entries)) {
			const wanted = msgid !== METADATA && ownEntry(ownEntry(named, context), msgid) !== undefined
			if (wanted && entry.msgstr.some((form) => form !== '')) {
				restoring(held, context, msgid, entry)
			}
		}
	}
	return po.compile(held, COMPILED).toString()
}

/**
 * Reports whether an exported answer settles a committed one.
 * @param entry - The committed answer.
 * @param arrived - The answer the platform exported.
 * @returns Whether the export answers at least as many forms as the committed entry, all filled.
 */
function settledBy(entry: GetTextTranslation, arrived: GetTextTranslation): boolean {
	return arrived.msgstr.length >= entry.msgstr.length
		&& arrived.msgstr.length > 0
		&& arrived.msgstr.every((form) => form !== '')
}

/**
 * Returns a catalogue without the messages an export already answers and nobody may overwrite.
 * @param source - The catalogue as committed.
 * @param exported - The catalogue the platform exported.
 * @returns The catalogue, holding only what the platform has not settled.
 */
export function withoutSettled(source: string, exported: string): string {
	const theirs = po.parse(exported).translations
	const held = po.parse(source)
	for (const [context, entries] of Object.entries(held.translations)) {
		for (const [msgid, entry] of Object.entries(entries)) {
			const arrived = ownEntry(ownEntry(theirs, context), msgid)
			if (msgid === METADATA || arrived === undefined || fuzzyOf(arrived)) {
				continue
			}
			if (settledBy(entry, arrived)) {
				delete held.translations[context][msgid]
				continue
			}
			entry.msgstr = entry.msgstr.map((form, at) => arrived.msgstr[at] || form)
		}
	}
	return po.compile(held, COMPILED).toString()
}

/** Answers is what one language's export carries, keyed by context and message. */
type Answers = Record<string, Record<string, { msgstr: string[], fuzzy: boolean }>>

/**
 * Returns the translations a catalogue holds, without the headers an export restamps.
 * @param source - The catalogue as PO text.
 * @returns The translations with their fuzzy state, keyed by context and message.
 */
function answersOf(source: string): Answers {
	const held: Answers = {}
	for (const [context, entries] of Object.entries(po.parse(source).translations).sort()) {
		held[context] = {}
		for (const [msgid, entry] of Object.entries(entries).sort()) {
			if (msgid !== METADATA) {
				held[context][msgid] = { msgstr: entry.msgstr, fuzzy: fuzzyOf(entry) }
			}
		}
	}
	return held
}

/**
 * Reports whether an incoming catalogue says anything the current one does not.
 * @param current - The catalogue as committed, or nothing when none is committed yet.
 * @param incoming - The catalogue the platform exported.
 * @returns True when a translation differs, false when only the export stamp moved.
 */
export function meaningfulChange(current: string | undefined, incoming: string): boolean {
	if (current === undefined) {
		return true
	}
	return JSON.stringify(answersOf(current)) !== JSON.stringify(answersOf(incoming))
}

/**
 * Returns how many messages a template names.
 * @param source - The template as POT text.
 * @returns The count of named messages.
 */
export function namedMessages(source: string): number {
	let held = 0
	for (const entries of Object.values(po.parse(source).translations)) {
		for (const msgid of Object.keys(entries)) {
			if (msgid !== METADATA) {
				held += 1
			}
		}
	}
	return held
}
