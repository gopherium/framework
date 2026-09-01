// SPDX-License-Identifier: Apache-2.0

import { __, setLocaleData } from '@wordpress/i18n'
import { expect, test } from 'vitest'

import { displayLocale, rememberLocale } from '../src/index.js'
import type { Catalog } from '../src/index.js'
import { resetLocale } from '../src/testing.js'

const CATALOG: Catalog = {
	'': { lang: 'es-ES', 'plural-forms': 'nplurals=2; plural=(n != 1);' },
	'Older posts': ['Entradas anteriores'],
}

test('takes every loaded domain back to its sources', () => {
	setLocaleData(CATALOG, 'gottext-reset-one')
	setLocaleData(CATALOG, 'gottext-reset-two')
	setLocaleData(CATALOG)

	resetLocale()

	expect(__('Older posts', 'gottext-reset-one')).toBe('Older posts')
	expect(__('Older posts', 'gottext-reset-two')).toBe('Older posts')
	expect(__('Older posts')).toBe('Older posts')
})

test('settles back on the locale it is handed', () => {
	rememberLocale('es-ES')

	resetLocale('fr-FR')

	expect(displayLocale()).toBe('fr-FR')
})

test('settles back on en-US when handed no locale', () => {
	rememberLocale('es-ES')

	resetLocale()

	expect(displayLocale()).toBe('en-US')
})
