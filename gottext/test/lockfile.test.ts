// SPDX-License-Identifier: Apache-2.0

import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect, test } from 'vitest'

import { pinnedVersions, resolvedVersions } from '../src/build.js'

const LOCKFILE = `
packages:

  '@wordpress/i18n@6.26.0':
    resolution: {integrity: sha512-a}

  left-pad@1.3.0:
    resolution: {integrity: sha512-b}

snapshots:

  '@wordpress/i18n@9.9.9': {}
`

test('names every version a lockfile resolves for a package', () => {
	expect(resolvedVersions(LOCKFILE, '@wordpress/i18n')).toEqual(['6.26.0'])
})

test('reads a package the lockfile names without quotes', () => {
	expect(resolvedVersions(LOCKFILE, 'left-pad')).toEqual(['1.3.0'])
})

test('names nothing for a package the lockfile does not resolve', () => {
	expect(resolvedVersions(LOCKFILE, 'not-a-package')).toEqual([])
})

test('reads a lockfile whose packages run to the end', () => {
	const held = "\npackages:\n  left-pad@1.3.0:\n    resolution: {integrity: sha512-x}\n"

	expect(resolvedVersions(held, 'left-pad')).toEqual(['1.3.0'])
})

test('names the version each package pins', () => {
	const root = mkdtempSync(join(tmpdir(), 'gottext-lock-'))
	mkdirSync(join(root, 'app'), { recursive: true })
	mkdirSync(join(root, 'kit'), { recursive: true })
	writeFileSync(join(root, 'app', 'package.json'), '{"dependencies":{"probe":"1.0.0"}}')
	writeFileSync(join(root, 'kit', 'package.json'), '{"dependencies":{"probe":"1.0.0"}}')

	expect(pinnedVersions(root, ['app', 'kit'], 'probe')).toEqual(['1.0.0', '1.0.0'])
})

test('passes over a package that declares no such dependency', () => {
	const root = mkdtempSync(join(tmpdir(), 'gottext-lock-'))
	mkdirSync(join(root, 'app'), { recursive: true })
	writeFileSync(join(root, 'app', 'package.json'), '{"dependencies":{"other":"2.0.0"}}')

	expect(pinnedVersions(root, ['app'], 'probe')).toEqual([])
})
