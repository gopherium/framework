// SPDX-License-Identifier: Apache-2.0

import { __, getLocaleData } from '@wordpress/i18n'
import { expect, test } from 'vitest'

import { displayLocale, rememberLocale, startLocale } from '../src/index.js'
import type { Catalog } from '../src/index.js'

const CATALOG: Catalog = {
	'': { lang: 'es-ES', 'plural-forms': 'nplurals=2; plural=(n != 1);' },
	'Older posts': ['Entradas anteriores'],
}

test('answers the locale its resolver settles on', async () => {
	const settled = await startLocale(async () => 'es-ES', [])

	expect(settled).toBe('es-ES')
})

test('remembers the settled locale for display', async () => {
	await startLocale(async () => 'es-ES', [])

	expect(displayLocale()).toBe('es-ES')
})

test('holds the default locale from the moment the start is asked', async () => {
	rememberLocale('de-DE')
	let release: (locale: string) => void = () => {}
	const pending = startLocale(
		() => new Promise((resolve) => { release = resolve }),
		[],
		{ defaultLocale: 'es-ES' },
	)

	expect(displayLocale()).toBe('es-ES')

	release('es-ES')
	await pending
})

test('sets a loaded catalogue under its domain before returning', async () => {
	await startLocale(async () => 'es-ES', [
		{ domain: 'gottext-probe', load: async () => CATALOG },
	])

	expect(getLocaleData('gottext-probe')['']).toBeDefined()
	expect(__('Older posts', 'gottext-probe')).toBe('Entradas anteriores')
})

test('leaves a domain untouched when its loader answers nothing', async () => {
	await startLocale(async () => 'es-ES', [
		{ domain: 'gottext-empty', load: async () => undefined },
	])

	expect(__('Older posts', 'gottext-empty')).toBe('Older posts')
})

test('loads nothing for the locale the sources are written in', async () => {
	const asked: string[] = []

	await startLocale(
		async () => 'en-US',
		[
			{
				domain: 'gottext-default',
				load: async (locale: string) => {
					asked.push(locale)
					return CATALOG
				},
			},
		],
		{ defaultLocale: 'en-US' },
	)

	expect(asked).toEqual([])
	expect(__('Older posts', 'gottext-default')).toBe('Older posts')
})

test('remembers the locale the sources are written in even though it loads nothing', async () => {
	const settled = await startLocale(async () => 'en-US', [], { defaultLocale: 'en-US' })

	expect(settled).toBe('en-US')
	expect(displayLocale()).toBe('en-US')
})

test('sets a catalogue naming no domain under the default domain', async () => {
	await startLocale(async () => 'es-ES', [{ load: async () => CATALOG }])

	expect(__('Older posts')).toBe('Entradas anteriores')
})

test('sets every catalogue it is handed', async () => {
	await startLocale(async () => 'es-ES', [
		{ domain: 'gottext-one', load: async () => CATALOG },
		{ domain: 'gottext-two', load: async () => CATALOG },
	])

	expect(__('Older posts', 'gottext-one')).toBe('Entradas anteriores')
	expect(__('Older posts', 'gottext-two')).toBe('Entradas anteriores')
})
