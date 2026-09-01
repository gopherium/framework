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

const PEER_LOCKFILE = `
packages:

  'plain@1.2.3':
    resolution: {integrity: sha512-c}

  '@scoped/tool@4.10.1(eslint@9.18.0)(typescript@5.7.3)':
    resolution: {integrity: sha512-d}

  peer-tool@2.0.0(eslint@9.18.0):
    resolution: {integrity: sha512-e}

snapshots:

  'plain@9.9.9': {}
`

const SPLIT_LOCKFILE = `
packages:

  '@scoped/tool@4.10.1(eslint@9.18.0)(typescript@5.7.3)':
    resolution: {integrity: sha512-f}

  '@scoped/tool@4.10.1(eslint@8.57.0)':
    resolution: {integrity: sha512-g}

  '@scoped/tool@4.11.0(eslint@9.18.0)':
    resolution: {integrity: sha512-h}
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

test('a peer qualified scoped key answers its bare version', () => {
	expect(resolvedVersions(PEER_LOCKFILE, '@scoped/tool')).toEqual(['4.10.1'])
})

test('a peer qualified unscoped key answers its bare version', () => {
	expect(resolvedVersions(PEER_LOCKFILE, 'peer-tool')).toEqual(['2.0.0'])
})

test('a plain key beside peer qualified keys keeps its version', () => {
	expect(resolvedVersions(PEER_LOCKFILE, 'plain')).toEqual(['1.2.3'])
})

test('a package resolved under different peer sets answers each bare version once', () => {
	expect(resolvedVersions(SPLIT_LOCKFILE, '@scoped/tool')).toEqual(['4.10.1', '4.11.0'])
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

const MULTI_DOCUMENT_LOCKFILE = `---
lockfileVersion: '9.0'

importers:

  .:
    packageManagerDependencies:
      pnpm:
        specifier: 12.2.1
        version: 12.2.1

packages:

  '@pnpm/exe.linux-x64@12.2.1':
    resolution: {integrity: sha512-c}

snapshots:

  '@pnpm/exe.linux-x64@12.2.1': {}
---
lockfileVersion: '9.0'

settings:
  autoInstallPeers: true

packages:

  '@wordpress/i18n@6.26.0':
    resolution: {integrity: sha512-a}

  left-pad@1.3.0:
    resolution: {integrity: sha512-b}

snapshots:

  '@wordpress/i18n@6.26.0': {}
`

test('reads the project packages of a lockfile the package manager writes in two documents', () => {
	expect(resolvedVersions(MULTI_DOCUMENT_LOCKFILE, '@wordpress/i18n')).toEqual(['6.26.0'])
	expect(resolvedVersions(MULTI_DOCUMENT_LOCKFILE, 'left-pad')).toEqual(['1.3.0'])
})

test('leaves the package manager out of the packages it reports', () => {
	expect(resolvedVersions(MULTI_DOCUMENT_LOCKFILE, '@pnpm/exe.linux-x64')).toEqual([])
})
