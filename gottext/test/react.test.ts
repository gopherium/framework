// SPDX-License-Identifier: Apache-2.0

import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, expect, test } from 'vitest'

import { formatDate, rememberFormatLocale, rememberLocale } from '../src/index.js'
import { FormatLocaleGate } from '../src/react.js'

afterEach(() => {
	rememberFormatLocale(undefined)
	rememberLocale('en-US')
})

/**
 * Shows one date as the screens write it.
 * @returns The date in a paragraph.
 */
function Dated() {
	return createElement('p', null, formatDate(new Date(2026, 8, 30)))
}

/**
 * Renders the gate around a dated screen, a loading line standing in until it opens.
 * @param locale - The format locale the settings named, undefined until they answer.
 * @param failed - Whether reading the settings failed.
 * @returns The markup the gate rendered.
 */
function renderGate(locale: string | undefined, failed: boolean): string {
	const loading = createElement('p', null, 'Loading settings')
	const children = createElement(Dated)
	return renderToStaticMarkup(createElement(FormatLocaleGate, { locale, failed, loading, children }))
}

test('holds the screens back while the settings load', () => {
	expect(renderGate(undefined, false)).toBe('<p>Loading settings</p>')
})

test('opens the screens in the format locale the settings name', () => {
	rememberLocale('en-US')

	expect(renderGate('de-DE', false)).toBe('<p>30.09.2026</p>')
})

test('remembers the format locale for every screen it opens', () => {
	renderGate('es-ES', false)

	expect(formatDate(new Date(2026, 8, 30))).toBe('30/09/2026')
})

test('opens the screens in the interface locale when the settings could not be read', () => {
	rememberLocale('en-US')

	expect(renderGate(undefined, true)).toBe('<p>09/30/2026</p>')
})

test('forgets a format locale an earlier answer named once reading the settings fails', () => {
	rememberLocale('en-US')
	renderGate('de-DE', false)

	expect(renderGate(undefined, true)).toBe('<p>09/30/2026</p>')
})
