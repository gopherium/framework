// SPDX-License-Identifier: Apache-2.0

import { resetLocaleData } from '@wordpress/i18n'

import { rememberLocale } from './display.js'
import { rememberFormatLocale } from './format.js'

/**
 * Takes every text domain, the display locale and the format locale back to their sources between tests.
 * @param defaultLocale - The locale to settle back on, en-US absent a choice.
 */
export function resetLocale(defaultLocale = 'en-US'): void {
	resetLocaleData({})
	rememberLocale(defaultLocale)
	rememberFormatLocale(undefined)
}
