// SPDX-License-Identifier: Apache-2.0

import { expect, test, vi } from 'vitest'

import { globCatalogs } from '../src/index.js'
import type { Catalog } from '../src/index.js'

const SPANISH: Catalog = {
	'': { lang: 'es-ES', 'plural-forms': 'nplurals=2; plural=(n != 1);' },
	'Older posts': ['Entradas anteriores'],
}

const FRENCH: Catalog = {
	'': { lang: 'fr-FR', 'plural-forms': 'nplurals=2; plural=(n > 1);' },
	'Older posts': ['Articles precedents'],
}

test('answers the catalogue whose file the locale names', async () => {
	const load = globCatalogs({
		'../languages/es-ES.json': async () => ({ default: SPANISH }),
		'../languages/fr-FR.json': async () => ({ default: FRENCH }),
	})

	expect(await load('es-ES')).toBe(SPANISH)
	expect(await load('fr-FR')).toBe(FRENCH)
})

test('answers nothing for a locale shipping no catalogue', async () => {
	const load = globCatalogs({ '../languages/es-ES.json': async () => ({ default: SPANISH }) })

	expect(await load('de-DE')).toBeUndefined()
})

test('reads the file stem however deep the path runs', async () => {
	const load = globCatalogs({
		'/srv/app/frontend/src/languages/editor/es-ES.json': async () => ({ default: SPANISH }),
	})

	expect(await load('es-ES')).toBe(SPANISH)
})

test('reads a whole path ending in no extension', async () => {
	const load = globCatalogs({ '../languages/es-ES': async () => ({ default: SPANISH }) })

	expect(await load('es-ES')).toBe(SPANISH)
})

test('leaves every other catalogue unread', async () => {
	const spanish = vi.fn(async () => ({ default: SPANISH }))
	const french = vi.fn(async () => ({ default: FRENCH }))
	const load = globCatalogs({
		'../languages/es-ES.json': spanish,
		'../languages/fr-FR.json': french,
	})

	await load('es-ES')

	expect(spanish).toHaveBeenCalledOnce()
	expect(french).not.toHaveBeenCalled()
})
