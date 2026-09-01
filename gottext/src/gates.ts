// SPDX-License-Identifier: Apache-2.0

import { po } from 'gettext-parser'

import { METADATA, held, keyOf } from './catalog.js'
import { fuzzyOf } from './merge.js'

/**
 * Returns every key a catalogue carries a filled translation for.
 * @param source - The catalogue as PO text.
 * @returns The keys answered.
 */
function answered(source: string): Set<string> {
	const held = new Set<string>()
	for (const [context, entries] of Object.entries(po.parse(source).translations)) {
		for (const [msgid, entry] of Object.entries(entries)) {
			if (msgid !== METADATA && entry.msgstr.length > 0 && entry.msgstr.every((form) => form !== '')) {
				held.add(keyOf(context, msgid))
			}
		}
	}
	return held
}

/**
 * Returns every message of a catalogue that still waits for a translation.
 * @param source - The catalogue as PO text.
 * @param template - The template naming every message that must be answered.
 * @returns The keys still waiting, in the order the template holds them.
 */
export function untranslated(source: string, template: string = source): string[] {
	const held = answered(source)
	const waiting: string[] = []
	for (const [context, entries] of Object.entries(po.parse(template).translations)) {
		for (const msgid of Object.keys(entries)) {
			if (msgid !== METADATA && !held.has(keyOf(context, msgid))) {
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

/** NAMED is a placeholder naming what goes into it. */
const NAMED = /%\(([A-Za-z_][A-Za-z0-9_]*)\)[bcdieEfgGosuxX]/g

/** BARE is a placeholder naming nothing. */
const BARE = /%(?:\d+\$)?[bcdieEfgGosuxX]/g

/**
 * Returns the placeholders a message names, each once.
 * @param message - The message to read.
 * @returns The names, in a settled order.
 */
function placeholders(message: string): string[] {
	return [...new Set([...message.matchAll(NAMED)].map((found) => found[1]))].sort()
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
			const answer = held(carried[context], msgid)
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
