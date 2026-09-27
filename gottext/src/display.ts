// SPDX-License-Identifier: Apache-2.0

let settled = 'en-US'

/** CALENDAR_DAY is the shape of a bare calendar day, such as 2026-09-01. */
const CALENDAR_DAY = /^\d{4}-\d{2}-\d{2}$/

/**
 * Stores the locale the interface stands in.
 * @param locale - The locale the interface settled on.
 */
export function rememberLocale(locale: string): void {
	settled = locale
}

/**
 * Returns the locale the interface stands in.
 * @returns The settled locale.
 */
export function displayLocale(): string {
	return settled
}

/**
 * Returns a moment as a date in the locale the interface stands in.
 * @param at - The moment, as a date, as the text a server stored, or as a bare calendar day shown on the day it names.
 * @param options - How much of the date to show.
 * @returns The date to show, empty when there is no moment.
 */
export function formatDate(at: Date | string, options?: Intl.DateTimeFormatOptions): string {
	if (at === '') {
		return ''
	}
	const day = typeof at === 'string' && CALENDAR_DAY.test(at)
	return new Date(at).toLocaleDateString(settled, day ? inUTC(options) : options)
}

/**
 * Returns the options a caller handed as a date format resolves them, showing UTC.
 * @param options - The options the caller handed, if any.
 * @returns The options a bare calendar day is shown with.
 */
function inUTC(options?: Intl.DateTimeFormatOptions): Intl.DateTimeFormatOptions {
	const resolved = new Intl.DateTimeFormat(settled, options).resolvedOptions()
	return { ...resolved, timeZone: 'UTC' } as Intl.DateTimeFormatOptions
}
