// SPDX-License-Identifier: Apache-2.0

import { afterEach, expect, test, vi } from 'vitest'

import { formatDate, rememberLocale } from '../src/index.js'

const AT = '2026-07-30T10:00:00Z'

const WEST_OF_UTC = 'America/Los_Angeles'

afterEach(() => {
	vi.unstubAllEnvs()
})

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

test('shows a bare calendar day on the day it names for a reader west of UTC', () => {
	vi.stubEnv('TZ', WEST_OF_UTC)
	rememberLocale('en-US')

	expect(formatDate('2026-09-01', { day: 'numeric' })).toBe('1')
})

test('shows a bare calendar day on the day it names whatever zone the options ask for', () => {
	rememberLocale('en-US')

	expect(formatDate('2026-09-01', { day: 'numeric', timeZone: WEST_OF_UTC })).toBe('1')
})

test('shows a bare calendar day handed no options on the day it names for a reader west of UTC', () => {
	vi.stubEnv('TZ', WEST_OF_UTC)
	rememberLocale('en-US')

	expect(formatDate('2026-09-01')).toBe('9/1/2026')
})

test('follows the options a bare calendar day is handed through their prototype', () => {
	rememberLocale('en-US')

	expect(formatDate('2026-09-01', Object.create({ year: 'numeric' }))).toBe('2026')
})

test('shows a full timestamp in the zone of a reader west of UTC', () => {
	vi.stubEnv('TZ', WEST_OF_UTC)
	rememberLocale('en-US')

	expect(formatDate('2026-09-01T03:00:00Z', { day: 'numeric' })).toBe('31')
})
