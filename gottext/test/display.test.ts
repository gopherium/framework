// SPDX-License-Identifier: Apache-2.0

import { expect, test } from 'vitest'

import { formatDate, rememberLocale } from '../src/index.js'

const AT = '2026-07-30T10:00:00Z'

test('shows nothing for an absent timestamp', () => {
	rememberLocale('en-US')

	expect(formatDate('')).toBe('')
})

test('shows a stored timestamp in the locale the interface settled on', () => {
	rememberLocale('en-US')
	const american = formatDate(AT)
	rememberLocale('de-DE')

	expect(formatDate(AT)).not.toBe(american)
})

test('shows a date it is handed as it shows the same instant given as text', () => {
	rememberLocale('en-US')

	expect(formatDate(new Date(AT))).toBe(formatDate(AT))
})

test('follows the options it is handed', () => {
	rememberLocale('en-US')

	expect(formatDate(AT, { year: 'numeric' })).toBe('2026')
})
