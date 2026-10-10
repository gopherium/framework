// SPDX-License-Identifier: Apache-2.0

import { po } from 'gettext-parser'
import type { GetTextTranslation } from 'gettext-parser'

import { METADATA, held, keyOf } from './catalog.js'
import { fuzzyOf } from './merge.js'

/**
 * Reports whether a translation fills every form its message requires.
 * @param entry - The translation the catalogue carries, if any.
 * @param forms - How many forms the message requires in this language.
 * @returns Whether every required form is answered.
 */
function answered(entry: GetTextTranslation | undefined, forms: number): boolean {
	return entry !== undefined && entry.msgstr.length >= forms && entry.msgstr.slice(0, forms).every((form) => form !== '')
}

/**
 * Returns the declared plural count, or the runtime's default when no usable count is named.
 * @param rule - The catalogue's plural rule, if it declares one.
 * @returns How many forms a plural message requires.
 */
function pluralCount(rule: string | undefined): number {
	const counted = /nplurals\s*=\s*(\d+)/.exec(rule ?? '')
	return counted === null ? 2 : Number(counted[1]) || 2
}

/**
 * Returns every message of a catalogue that still waits for a translation.
 * Plural messages need the catalogue's declared form count, or two when it names no usable count.
 * @param source - The catalogue as PO text.
 * @param template - The template naming every message that must be answered.
 * @returns The keys still waiting, in the order the template holds them.
 */
export function untranslated(source: string, template: string = source): string[] {
	const carried = po.parse(source)
	const forms = pluralCount(carried.headers?.['Plural-Forms'])
	const waiting: string[] = []
	for (const [context, entries] of Object.entries(po.parse(template).translations)) {
		for (const [msgid, entry] of Object.entries(entries)) {
			const answer = held(held(carried.translations, context), msgid)
			if (msgid !== METADATA && !answered(answer, entry.msgid_plural === undefined ? 1 : forms)) {
				waiting.push(keyOf(context, msgid))
			}
		}
	}
	return waiting
}

/**
 * Returns every message a catalogue carries that the template does not name.
 * @param source - The catalogue as PO text.
 * @param template - The template naming every message the catalogue may carry.
 * @returns The keys carried without a place in the template.
 */
export function orphaned(source: string, template: string): string[] {
	const named = po.parse(template).translations
	const carried: string[] = []
	for (const [context, entries] of Object.entries(po.parse(source).translations)) {
		for (const msgid of Object.keys(entries)) {
			if (msgid !== METADATA && held(named[context], msgid) === undefined) {
				carried.push(keyOf(context, msgid))
			}
		}
	}
	return carried
}

/**
 * Returns every message whose answer still carries the fuzzy flag.
 * @param source - The catalogue as PO text.
 * @returns The keys answered but not yet reviewed.
 */
export function unreviewed(source: string): string[] {
	const waiting: string[] = []
	for (const [context, entries] of Object.entries(po.parse(source).translations)) {
		for (const [msgid, entry] of Object.entries(entries)) {
			if (msgid !== METADATA && fuzzyOf(entry) && entry.msgstr.some((form) => form !== '')) {
				waiting.push(keyOf(context, msgid))
			}
		}
	}
	return waiting
}

/** NAMED is a placeholder naming what goes into it, with any flags, width, precision, and length it carries. */
const NAMED = /%%|%\(([A-Za-z_][A-Za-z0-9_]*)\)[+0#-]*\d*(?:\.\d+)?(?:ll|[lhqL])?[bcdieEfgGosuxX]/g

/** BARE is a placeholder naming nothing, with any flags, width, precision, and length it carries. */
const BARE = /%%|%(?:\d+\$)?[+0#-]*\d*(?:\.\d+)?(?:ll|[lhqL])?[bcdieEfgGosuxX]/g

/**
 * Returns the named placeholders a message carries, each once and whole.
 * @param message - The message to read.
 * @returns The placeholders, in a settled order.
 */
function placeholders(message: string): string[] {
	return [...new Set([...message.matchAll(NAMED)].map((found) => found[0]))].sort()
}

/**
 * Returns the placeholders a message carries that name nothing, in a settled order.
 * @param message - The message to read.
 * @returns The bare placeholders, sorted.
 */
function bare(message: string): string[] {
	return [...message.replace(NAMED, '').matchAll(BARE)].map((found) => found[0]).sort()
}

/**
 * Returns whether two placeholder lists hold the same members.
 * @param left - The list on the left.
 * @param right - The list on the right.
 * @returns Whether both hold the same members.
 */
function alike(left: string[], right: string[]): boolean {
	return left.length === right.length && left.every((held, at) => held === right[at])
}

/**
 * Returns whether a translated form answers its message with the placeholders that message names.
 * @param form - The translated form.
 * @param message - The message the form answers.
 * @returns Whether the form matches.
 */
function answersPlaceholders(form: string, message: string): boolean {
	if (form === '') {
		return true
	}
	return alike(placeholders(form), placeholders(message)) && alike(bare(form), bare(message))
}

/** NUMBER is the conversion a placeholder ends in when it writes a number rather than text. */
const NUMBER = /[deEfgGiu]$/

/**
 * Returns whether a message writes a number through one of its placeholders.
 * @param message - The message to read.
 * @returns Whether a placeholder writes a number.
 */
function writesNumber(message: string): boolean {
	return [...placeholders(message), ...bare(message)].some((found) => NUMBER.test(found))
}

/**
 * Returns every message that writes a number through a placeholder rather than as text a number formatter wrote.
 * @param template - The template naming every message.
 * @returns The keys writing a bare number, in the order the template holds them.
 */
export function unformatted(template: string): string[] {
	const found: string[] = []
	for (const [context, entries] of Object.entries(po.parse(template).translations)) {
		for (const [msgid, entry] of Object.entries(entries)) {
			if (writesNumber(msgid) || writesNumber(entry.msgid_plural ?? '')) {
				found.push(keyOf(context, msgid))
			}
		}
	}
	return found
}

/**
 * Returns every message whose translation names placeholders its message does not.
 * @param source - The catalogue as PO text.
 * @param template - The template naming every message and the placeholders it carries.
 * @returns The keys whose translation would not render.
 */
export function mismatched(source: string, template: string): string[] {
	const carried = po.parse(source).translations
	const broken: string[] = []
	for (const [context, entries] of Object.entries(po.parse(template).translations)) {
		for (const [msgid, entry] of Object.entries(entries)) {
			const answer = held(held(carried, context), msgid)
			if (msgid === METADATA || answer === undefined) {
				continue
			}
			const plural = entry.msgid_plural ?? msgid
			const matches = answer.msgstr.every((form, at) =>
				answersPlaceholders(form, at === 0 ? msgid : plural))
			if (!matches) {
				broken.push(keyOf(context, msgid))
			}
		}
	}
	return broken
}
