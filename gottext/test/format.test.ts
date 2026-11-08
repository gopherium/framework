// SPDX-License-Identifier: Apache-2.0

import { afterEach, expect, test, vi } from 'vitest'

import {
	formatDate,
	formatList,
	formatMoney,
	formatNumber,
	formatTime,
	formatWeekday,
	rememberFormatLocale,
	rememberLocale,
} from '../src/index.js'

afterEach(() => {
	vi.unstubAllEnvs()
	rememberFormatLocale(undefined)
	rememberLocale('en-US')
})

test('writes a date day first with a two digit month and a four digit year', () => {
	rememberFormatLocale('es-ES')

	expect(formatDate(new Date(2026, 8, 30, 23, 30))).toBe('30/09/2026')
	expect(formatDate(new Date(2026, 6, 6))).toBe('06/07/2026')
})

test('writes a moment a server stored on the local day it fell', () => {
	rememberFormatLocale('es-ES')
	vi.stubEnv('TZ', 'Pacific/Honolulu')

	expect(formatDate('2026-09-30T09:05:00Z')).toBe('29/09/2026')
})

test('writes a bare calendar day on the day it names in every time zone', () => {
	rememberFormatLocale('es-ES')
	vi.stubEnv('TZ', 'America/New_York')

	expect(formatDate('2026-09-30')).toBe('30/09/2026')
})

test('writes a bare calendar day in the digits and calendar of the format locale whatever the interface locale', () => {
	rememberLocale('ar-EG')
	rememberFormatLocale('es-ES')

	expect(formatDate('2026-09-30')).toBe('30/09/2026')
})

test('writes a date with the options it is handed in the format locale', () => {
	rememberLocale('en-US')
	rememberFormatLocale('es-ES')

	expect(formatDate(new Date(2026, 6, 30), { month: 'long' })).toBe('julio')
})

test('writes a bare calendar day with only the parts it is handed in the format locale', () => {
	rememberFormatLocale('es-ES')

	expect(formatDate('2026-09-30', { month: 'long', year: 'numeric' })).toBe('septiembre de 2026')
	expect(formatDate('2026-09-30', { month: 'short', year: 'numeric' })).toBe(
		formatDate('2026-09-30T12:00:00Z', { month: 'short', year: 'numeric' }),
	)
})

test('keeps the year of a bare calendar day in a calendar of its own', () => {
	rememberFormatLocale('es-ES-u-ca-chinese')
	vi.stubEnv('TZ', 'UTC')

	expect(formatDate('2026-08-20')).toContain('2026')
	expect(formatDate('2026-08-20')).toBe(formatDate('2026-08-20T12:00:00Z'))
})

test.each<Intl.DateTimeFormatOptions>([
	{ calendar: 'chinese' },
	{ numberingSystem: 'arab' },
	{ era: 'long', year: 'numeric' },
	{ weekday: 'long', day: 'numeric' },
	{ day: 'numeric', hour: 'numeric' },
	{ day: 'numeric', hour: 'numeric', minute: '2-digit' },
	{ day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' },
	{ day: 'numeric', second: '2-digit', fractionalSecondDigits: 3 },
	{ day: 'numeric', hour: 'numeric', dayPeriod: 'long' },
	{ day: 'numeric', hour: 'numeric', hour12: false },
	{ day: 'numeric', hour: 'numeric', hourCycle: 'h23' },
	{ day: 'numeric', timeZoneName: 'short' },
])('writes a bare calendar day handed %o as midnight UTC on that day', (options) => {
	rememberFormatLocale('en-US')
	vi.stubEnv('TZ', 'UTC')

	expect(formatDate('2026-09-01', options)).toBe(formatDate('2026-09-01T00:00:00Z', options))
})

test('refuses a time style on a bare calendar day as on a timestamp', () => {
	rememberFormatLocale('en-US')

	expect(() => formatDate('2026-09-01', { timeStyle: 'short' })).toThrow(TypeError)
	expect(() => formatDate('2026-09-01T00:00:00Z', { timeStyle: 'short' })).toThrow(TypeError)
})

test('writes a time as two digit hours and minutes on the es-ES clock', () => {
	rememberFormatLocale('es-ES')
	vi.stubEnv('TZ', 'UTC')

	expect(formatTime(new Date(2026, 8, 30, 21, 5))).toBe('21:05')
	expect(formatTime('2026-09-30T09:05:00Z')).toBe('09:05')
})

test('writes a bare wall clock time as the time it names in every time zone', () => {
	rememberFormatLocale('es-ES')
	vi.stubEnv('TZ', 'America/Los_Angeles')

	expect(formatTime('09:30')).toBe('09:30')
	expect(formatTime('21:05')).toBe('21:05')
	expect(formatTime('00:00')).toBe('00:00')
	expect(formatTime('23:59')).toBe('23:59')
})

test('writes a bare wall clock time on the en-US clock as the time it names', () => {
	rememberFormatLocale('en-US')
	vi.stubEnv('TZ', 'Asia/Tokyo')

	expect(formatTime('09:30')).toBe('09:30 AM')
	expect(formatTime('21:05')).toBe('09:05 PM')
	expect(formatTime('00:00')).toBe('12:00 AM')
})

test('writes a timestamp a server stored on the reader clock, with or without a zone', () => {
	rememberFormatLocale('es-ES')
	vi.stubEnv('TZ', 'America/Los_Angeles')

	expect(formatTime('2026-09-30T09:05:00Z')).toBe('02:05')
	expect(formatTime('2026-09-30T09:05')).toBe('09:05')
})

test('writes nothing for an absent moment', () => {
	rememberFormatLocale('es-ES')

	expect(formatDate('')).toBe('')
	expect(formatTime('')).toBe('')
	expect(formatWeekday('')).toBe('')
})

test('groups the thousands of every number with a dot and writes decimals after a comma', () => {
	rememberFormatLocale('es-ES')

	expect(formatNumber(1234)).toBe('1.234')
	expect(formatNumber(1234.56)).toBe('1.234,56')
	expect(formatNumber(1234567)).toBe('1.234.567')
	expect(formatNumber(999)).toBe('999')
})

test('writes a number to the precision a screen asks for', () => {
	rememberFormatLocale('es-ES')

	expect(formatNumber(1.55, { maximumFractionDigits: 1 })).toBe('1,6')
})

test('groups the thousands even when the options a screen hands say otherwise', () => {
	rememberFormatLocale('es-ES')

	expect(formatNumber(1234, { useGrouping: false })).toBe('1.234')
})

test('writes an amount of euros after its number', () => {
	rememberFormatLocale('es-ES')

	expect(formatMoney(1234.56, 'EUR')).toBe('1.234,56 €')
})

test('keeps the format locale whatever language the interface stands in', () => {
	rememberLocale('en-US')
	rememberFormatLocale('es-ES')

	expect(formatDate(new Date(2026, 8, 30))).toBe('30/09/2026')
	expect(formatNumber(1234.56)).toBe('1.234,56')
})

test('writes the conventions of the format locale it is handed', () => {
	rememberFormatLocale('de-DE')

	expect(formatDate(new Date(2026, 8, 30))).toBe('30.09.2026')

	rememberFormatLocale('en-GB')

	expect(formatNumber(1234.5)).toBe('1,234.5')
})

test('writes in the interface locale until a format locale is remembered', () => {
	rememberLocale('en-US')

	expect(formatDate(new Date(2026, 8, 30))).toBe('09/30/2026')
	expect(formatNumber(1234.5)).toBe('1,234.5')
})

test('writes in the interface locale again once the format locale is forgotten', () => {
	rememberLocale('en-US')
	rememberFormatLocale('es-ES')

	rememberFormatLocale(undefined)

	expect(formatDate(new Date(2026, 8, 30))).toBe('09/30/2026')
})

test('names a weekday in the interface language whatever the format locale', () => {
	rememberFormatLocale('en-GB')
	rememberLocale('es-ES')

	expect(formatWeekday('2026-09-30')).toBe('miércoles')

	rememberLocale('en-US')

	expect(formatWeekday(new Date(2026, 8, 30))).toBe('Wednesday')
})

test('names the weekday a bare calendar day falls on in every time zone', () => {
	rememberLocale('en-US')
	vi.stubEnv('TZ', 'America/New_York')

	expect(formatWeekday('2026-09-30')).toBe('Wednesday')
})

test('joins a list the way the interface language writes one', () => {
	rememberFormatLocale('de-DE')
	rememberLocale('es-ES')

	expect(formatList(['Fecha de nacimiento', 'Talla de calzado'])).toBe('Fecha de nacimiento y Talla de calzado')

	rememberLocale('en-US')

	expect(formatList(['Birth date', 'Shoe size', 'Height'])).toBe('Birth date, Shoe size, Height')
})
