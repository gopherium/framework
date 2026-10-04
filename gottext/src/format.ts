// SPDX-License-Identifier: Apache-2.0

import { displayLocale } from './display.js'

/** named holds the locale dates, times, numbers and money are written in, once one is remembered. */
let named: string | undefined

/** CALENDAR_DAY is the shape of a bare calendar day, such as 2026-09-01. */
const CALENDAR_DAY = /^\d{4}-\d{2}-\d{2}$/

/** DAY is how a date is written unless the caller asks otherwise. */
const DAY: Intl.DateTimeFormatOptions = { day: '2-digit', month: '2-digit', year: 'numeric' }

/** CLOCK is how a time is written. */
const CLOCK: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' }

/**
 * Stores the locale dates, times, numbers and money are written in.
 * @param locale - The locale to write in, or undefined to forget it.
 */
export function rememberFormatLocale(locale: string | undefined): void {
	named = locale
}

/**
 * Returns the locale dates, times, numbers and money are written in.
 * @returns The remembered format locale, the interface locale until one is remembered.
 */
function formatLocale(): string {
	return named ?? displayLocale()
}

/**
 * Returns a moment as a date in a locale, a bare calendar day kept on the day it names.
 * @param at - The moment, the text a server stored, or a bare calendar day.
 * @param locale - The locale to write in.
 * @param options - How much of the date to show.
 * @returns The date to show, empty when there is no moment.
 */
function writeDay(at: Date | string, locale: string, options: Intl.DateTimeFormatOptions): string {
	if (at === '') {
		return ''
	}
	const day = typeof at === 'string' && CALENDAR_DAY.test(at)
	return new Date(at).toLocaleDateString(locale, day ? inUTC(locale, options) : options)
}

/**
 * Returns the options a caller handed as a date format resolves them, showing UTC.
 * @param locale - The locale the date is written in.
 * @param options - The options the caller handed.
 * @returns The options a bare calendar day is shown with.
 */
function inUTC(locale: string, options: Intl.DateTimeFormatOptions): Intl.DateTimeFormatOptions {
	const resolved = new Intl.DateTimeFormat(locale, options).resolvedOptions()
	return { ...resolved, timeZone: 'UTC' } as Intl.DateTimeFormatOptions
}

/**
 * Returns a moment as a date in the format locale.
 * @param at - The moment, the text a server stored, or a bare calendar day.
 * @param options - How much of the date to show, a two digit day and month and a four digit year by default.
 * @returns The date, such as 30/09/2026, empty when there is no moment.
 */
export function formatDate(at: Date | string, options: Intl.DateTimeFormatOptions = DAY): string {
	return writeDay(at, formatLocale(), options)
}

/**
 * Returns a moment as a time of day in the format locale.
 * @param at - The moment, or the text a server stored.
 * @returns The time, such as 09:05, empty when there is no moment.
 */
export function formatTime(at: Date | string): string {
	return at === '' ? '' : new Date(at).toLocaleTimeString(formatLocale(), CLOCK)
}

/**
 * Returns the name of the weekday a moment falls on, in the interface language.
 * @param at - The moment, the text a server stored, or a bare calendar day.
 * @returns The weekday, such as Wednesday, empty when there is no moment.
 */
export function formatWeekday(at: Date | string): string {
	return writeDay(at, displayLocale(), { weekday: 'long' })
}

/**
 * Returns a number in the format locale, its thousands always grouped.
 * @param value - The number.
 * @param options - How many decimals and which style to write it with.
 * @returns The number, such as 1.234,56.
 */
export function formatNumber(value: number, options: Intl.NumberFormatOptions = {}): string {
	return new Intl.NumberFormat(formatLocale(), { ...options, useGrouping: 'always' }).format(value)
}

/**
 * Returns the items as one list in the interface language.
 * @param items - The words to list, in order.
 * @returns The list, such as Birth date, Shoe size.
 */
export function formatList(items: readonly string[]): string {
	return new Intl.ListFormat(displayLocale(), { type: 'unit' }).format(items)
}

/**
 * Returns an amount of money in the format locale.
 * @param amount - The amount.
 * @param currency - The ISO 4217 code of the currency the amount is in.
 * @returns The amount, such as 1.234,56 €.
 */
export function formatMoney(amount: number, currency: string): string {
	return formatNumber(amount, { style: 'currency', currency })
}
