// SPDX-License-Identifier: Apache-2.0

let settled = 'en-US'

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
 * @param at - The moment, as a date or as the text a server stored.
 * @param options - How much of the date to show.
 * @returns The date to show, empty when there is no moment.
 */
export function formatDate(at: Date | string, options?: Intl.DateTimeFormatOptions): string {
	return at === '' ? '' : new Date(at).toLocaleDateString(settled, options)
}
