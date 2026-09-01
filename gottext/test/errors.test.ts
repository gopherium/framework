// SPDX-License-Identifier: Apache-2.0

import { expect, test } from 'vitest'

import { errorText } from '../src/index.js'

const TEMPLATES = {
	first_out_of_range: 'Ask for between %(min)d and %(max)d at a time.',
	name_taken: 'That name is already taken.',
	locale_unknown: 'AlphOne does not speak %(wanted)s yet.',
}

const FALLBACK = 'Something went wrong. Try again.'

test('fills the template its code names with the data the answer carries', () => {
	const shown = errorText(
		{ message: 'graph: first must be between 1 and 200', code: 'first_out_of_range', meta: { min: 1, max: 200 } },
		TEMPLATES,
		FALLBACK,
	)

	expect(shown).toBe('Ask for between 1 and 200 at a time.')
})

test('shows a template naming no values as it stands', () => {
	const shown = errorText({ message: 'fields: name taken', code: 'name_taken' }, TEMPLATES, FALLBACK)

	expect(shown).toBe('That name is already taken.')
})

test('speaks the answer when no template holds its code', () => {
	const shown = errorText(
		{ message: 'the server said something new', code: 'a_code_from_the_future' },
		TEMPLATES,
		FALLBACK,
	)

	expect(shown).toBe('the server said something new')
})

test('speaks the answer when it names no code at all', () => {
	const shown = errorText({ message: 'the server answered 500' }, TEMPLATES, FALLBACK)

	expect(shown).toBe('the server answered 500')
})

test('speaks the answer when the template asks for a value the answer lacks', () => {
	const shown = errorText(
		{ message: 'graph: first must be between 1 and 200', code: 'first_out_of_range', meta: { min: 1 } },
		TEMPLATES,
		FALLBACK,
	)

	expect(shown).toBe('graph: first must be between 1 and 200')
})

test('speaks the answer when the template asks for values and none arrived', () => {
	const shown = errorText({ message: 'locale refused', code: 'locale_unknown' }, TEMPLATES, FALLBACK)

	expect(shown).toBe('locale refused')
})

test('speaks the answer when a template asking for a decimal lacks its value', () => {
	const shown = errorText(
		{ message: 'the balance is short', code: 'short', meta: { held: 9 } },
		{ short: 'You hold %(held).2f of the %(needed).2f asked for.' },
		FALLBACK,
	)

	expect(shown).toBe('the balance is short')
})

test('fills a template rounding each value to the decimals it asks for', () => {
	const shown = errorText(
		{ message: 'raw', code: 'short', meta: { held: 9, needed: 12.5 } },
		{ short: 'You hold %(held)05.2f of the %(needed).1f asked for.' },
		FALLBACK,
	)

	expect(shown).toBe('You hold 9.00 of the 12.5 asked for.')
})

test('speaks the answer when its code names something every object inherits', () => {
	for (const code of ['constructor', 'toString', 'hasOwnProperty']) {
		const shown = errorText({ message: 'the server said this', code }, TEMPLATES, FALLBACK)

		expect(shown).toBe('the server said this')
	}
})

test('speaks the answer when a template names a value every object inherits', () => {
	const shown = errorText(
		{ message: 'the server said this', code: 'inherited', meta: {} },
		{ inherited: 'Value is %(constructor)s.' },
		FALLBACK,
	)

	expect(shown).toBe('the server said this')
})

test('falls back to the words the caller supplied when the answer says nothing', () => {
	const shown = errorText({ message: '' }, TEMPLATES, FALLBACK)

	expect(shown).toBe(FALLBACK)
})

test('prefers a filled template over an answer that says nothing', () => {
	const shown = errorText({ message: '', code: 'name_taken' }, TEMPLATES, FALLBACK)

	expect(shown).toBe('That name is already taken.')
})
