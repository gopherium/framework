// SPDX-License-Identifier: Apache-2.0

import { __, getLocaleData, resetLocaleData, setLocaleData } from '@wordpress/i18n'
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

test('keeps the display locale while a catalogue is still loading', async () => {
	rememberLocale('en-US')
	let release: (catalog: Catalog) => void = () => {}
	const pending = startLocale(async () => 'es-ES', [
		{ domain: 'gottext-slow', load: () => new Promise((resolve) => { release = resolve }) },
	])
	await Promise.resolve()
	await Promise.resolve()

	expect(displayLocale()).toBe('en-US')

	release(CATALOG)
	await pending
	expect(displayLocale()).toBe('es-ES')
})

test('keeps the display locale when a loader refuses', async () => {
	rememberLocale('en-US')
	const refusal = new Error('the catalogue endpoint refused')
	const pending = startLocale(async () => 'de-DE', [
		{ domain: 'gottext-broken', load: () => Promise.reject(refusal) },
	])

	await expect(pending).rejects.toBe(refusal)
	expect(displayLocale()).toBe('en-US')
})

test('a superseded start drops its catalogues and locale commit', async () => {
	let releaseOlder: (catalog: Catalog) => void = () => {}
	const olderCatalog: Catalog = {
		'': { lang: 'de-DE', 'plural-forms': 'nplurals=2; plural=(n != 1);' },
		'Older posts': ['answered by the older start'],
	}
	const older = startLocale(async () => 'de-DE', [
		{ domain: 'gottext-race', load: () => new Promise((resolve) => { releaseOlder = resolve }) },
	])
	const newerCatalog: Catalog = {
		'': { lang: 'fr-FR', 'plural-forms': 'nplurals=2; plural=(n != 1);' },
		'Older posts': ['answered by the newer start'],
	}
	const newer = startLocale(async () => 'fr-FR', [
		{ domain: 'gottext-race', load: async () => newerCatalog },
	])

	await newer
	releaseOlder(olderCatalog)
	await older

	expect(__('Older posts', 'gottext-race')).toBe('answered by the newer start')
	expect(displayLocale()).toBe('fr-FR')
})

test('a reset naming one domain clears every loaded domain', () => {
	setLocaleData(CATALOG, 'gottext-reset-named')
	setLocaleData(CATALOG, 'gottext-reset-other')
	resetLocaleData({}, 'gottext-reset-named')

	expect(__('Older posts', 'gottext-reset-named')).toBe('Older posts')
	expect(__('Older posts', 'gottext-reset-other')).toBe('Older posts')
})

test('a switch clears a domain whose loader answers nothing', async () => {
	await startLocale(async () => 'es-ES', [
		{ domain: 'gottext-stale', load: async () => CATALOG },
	])
	await startLocale(async () => 'de-DE', [
		{ domain: 'gottext-stale', load: async () => undefined },
	])

	expect(__('Older posts', 'gottext-stale')).toBe('Older posts')
})

test('a switch to the default locale clears every configured domain', async () => {
	await startLocale(async () => 'es-ES', [
		{ domain: 'gottext-home-one', load: async () => CATALOG },
		{ domain: 'gottext-home-two', load: async () => CATALOG },
	])
	await startLocale(
		async () => 'en-US',
		[
			{ domain: 'gottext-home-one', load: async () => CATALOG },
			{ domain: 'gottext-home-two', load: async () => CATALOG },
		],
		{ defaultLocale: 'en-US' },
	)

	expect(__('Older posts', 'gottext-home-one')).toBe('Older posts')
	expect(__('Older posts', 'gottext-home-two')).toBe('Older posts')
})

test('a switch replaces a catalogue rather than merging into it', async () => {
	const fuller: Catalog = {
		'': { lang: 'es-ES', 'plural-forms': 'nplurals=2; plural=(n != 1);' },
		'Older posts': ['Entradas anteriores'],
		'Newer posts': ['Entradas siguientes'],
	}
	const narrower: Catalog = {
		'': { lang: 'es-ES', 'plural-forms': 'nplurals=2; plural=(n != 1);' },
		'Older posts': ['Entradas anteriores'],
	}
	await startLocale(async () => 'es-ES', [{ domain: 'gottext-swap', load: async () => fuller }])
	await startLocale(async () => 'es-ES', [{ domain: 'gottext-swap', load: async () => narrower }])

	expect(__('Newer posts', 'gottext-swap')).toBe('Newer posts')
	expect(__('Older posts', 'gottext-swap')).toBe('Entradas anteriores')
})
