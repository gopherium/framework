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
