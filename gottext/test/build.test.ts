// SPDX-License-Identifier: Apache-2.0

import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { expect, test } from 'vitest'

import {
	compileCatalog,
	goMessages,
	goString,
	mismatched,
	orphaned,
	pot,
	serializeCatalog,
	unreviewed,
	untranslated,
} from '../src/build.js'

/**
 * Returns a root holding the given files, each written under it.
 * @param files - The files to write, keyed by path under the root.
 * @returns The root the files sit under.
 */
function rootWith(files: Record<string, string>): string {
	const root = mkdtempSync(join(tmpdir(), 'gottext-'))
	for (const [path, body] of Object.entries(files)) {
		const target = join(root, path)
		mkdirSync(dirname(target), { recursive: true })
		writeFileSync(target, body)
	}
	return root
}

const SOURCES = ['src/**/*.ts']

test('names every message the sources declare', () => {
	const root = rootWith({ 'src/a.ts': "__('Older posts', 'probe')\n" })

	const held = pot({ domain: 'probe', root, sources: SOURCES }).toString()

	expect(held).toContain('msgid "Older posts"')
})

test('names the context a message stands in', () => {
	const root = rootWith({ 'src/a.ts': "_x('Draft', 'status', 'probe')\n" })

	const held = pot({ domain: 'probe', root, sources: SOURCES }).toString()

	expect(held).toContain('msgctxt "status"')
	expect(held).toContain('msgid "Draft"')
})

test('names both forms of a plural message', () => {
	const root = rootWith({ 'src/a.ts': "_n('%(count)d post', '%(count)d posts', 2, 'probe')\n" })

	const held = pot({ domain: 'probe', root, sources: SOURCES }).toString()

	expect(held).toContain('msgid_plural "%(count)d posts"')
	expect(held).toContain('msgstr[1] ""')
})

test('carries the domain it was given in its header', () => {
	const root = rootWith({ 'src/a.ts': "__('Older posts', 'probe')\n" })

	const held = pot({ domain: 'probe', root, sources: SOURCES }).toString()

	expect(held).toContain('X-Domain: probe')
	expect(held).toContain('Project-Id-Version: probe')
})

test('names its messages in a settled order however the sources are read', () => {
	const root = rootWith({
		'src/b.ts': "__('Zebra', 'probe')\n__('Apple', 'probe')\n",
	})

	const held = pot({ domain: 'probe', root, sources: SOURCES }).toString()

	expect(held.indexOf('msgid "Apple"')).toBeLessThan(held.indexOf('msgid "Zebra"'))
})

test('keeps the safety baseline whatever exclusions it is handed', () => {
	const root = rootWith({
		'src/a.ts': "__('Real message', 'probe')\n",
		'src/a.d.ts': "__('Declared message', 'probe')\n",
		'src/a.test.ts': "__('Suite message', 'probe')\n",
		'src/node_modules/thing/v.ts': "__('Vendored message', 'probe')\n",
		'src/keep/x.ts': "__('Kept message', 'probe')\n",
		'src/skip/x.ts': "__('Skipped message', 'probe')\n",
	})

	const held = pot({ domain: 'probe', root, sources: SOURCES, ignored: ['**/skip/**'] }).toString()

	expect(held).toContain('Real message')
	expect(held).toContain('Kept message')
	expect(held).not.toContain('Skipped message')
	expect(held).not.toContain('Declared message')
	expect(held).not.toContain('Suite message')
	expect(held).not.toContain('Vendored message')
})

test('walks no Go roots unless it is given some', () => {
	const root = rootWith({ 'src/a.ts': "__('Older posts', 'probe')\n" })

	expect(() => pot({ domain: 'probe', root, sources: SOURCES })).not.toThrow()
})

test('names the messages a Go marker declares', () => {
	const root = rootWith({
		'src/a.ts': "__('Older posts', 'probe')\n",
		'internal/thing.go': 'package thing\n\nvar held = i18n.Msgid("Saved")\n',
	})

	const held = pot({ domain: 'probe', root, sources: SOURCES, goRoots: ['internal'] }).toString()

	expect(held).toContain('msgid "Saved"')
})

test('names the messages a Go template asks for', () => {
	const root = rootWith({
		'src/a.ts': "__('Older posts', 'probe')\n",
		'internal/page.html': '<p>{{T.Get "Read more"}}</p>\n',
	})

	const held = pot({ domain: 'probe', root, sources: SOURCES, goRoots: ['internal'] }).toString()

	expect(held).toContain('msgid "Read more"')
})

test('passes over a Go test file', () => {
	const root = rootWith({
		'src/a.ts': "__('Older posts', 'probe')\n",
		'internal/thing_test.go': 'package thing\n\nvar held = i18n.Msgid("Only in a test")\n',
	})

	const held = pot({ domain: 'probe', root, sources: SOURCES, goRoots: ['internal'] }).toString()

	expect(held).not.toContain('Only in a test')
})

test('reads a Go root through the directories nested under it', () => {
	const root = rootWith({
		'src/a.ts': "__('Older posts', 'probe')\n",
		'internal/site/templates/page.html': '<p>{{T.Get "Read more"}}</p>\n',
	})

	const held = pot({ domain: 'probe', root, sources: SOURCES, goRoots: ['internal'] }).toString()

	expect(held).toContain('msgid "Read more"')
})

test('walks past an installed tree inside a Go root', () => {
	const root = rootWith({
		'src/a.ts': "__('Older posts', 'probe')\n",
		'internal/node_modules/thing/x.go': 'package thing\n\nvar held = Msgid("Vendored")\n',
	})

	const held = pot({ domain: 'probe', root, sources: SOURCES, goRoots: ['internal'] }).toString()

	expect(held).not.toContain('Vendored')
})

test('reads the escapes a Go string carries', () => {
	expect(goString('a \\"quoted\\" word', 'thing.go')).toBe('a "quoted" word')
})

test('refuses a Go string holding an escape it cannot read', () => {
	expect(() => goString('a \\q word', 'thing.go')).toThrow(/escape/)
})

test('answers the messages a Go root declares', () => {
	const root = rootWith({ 'internal/thing.go': 'package thing\n\nvar held = Msgid("Saved")\n' })

	expect(goMessages(root, ['internal'])).toEqual(['Saved'])
})

const CATALOGUE = `msgid ""
msgstr ""
"Language: es-ES\\n"
"Plural-Forms: nplurals=2; plural=(n != 1);\\n"

msgid "Older posts"
msgstr "Entradas anteriores"
`

test('compiles a catalogue with its metadata entry first', () => {
	const held = compileCatalog(CATALOGUE)

	expect(Object.keys(held)[0]).toBe('')
	expect(held['']).toEqual({ lang: 'es-ES', 'plural-forms': 'nplurals=2; plural=(n != 1);' })
	expect(held['Older posts']).toEqual(['Entradas anteriores'])
})

test('joins a context to its message in the compiled key', () => {
	const source = `msgid ""
msgstr ""

msgctxt "status"
msgid "Draft"
msgstr "Borrador"
`

	expect(Object.keys(compileCatalog(source))).toContain('statusDraft')
})

const MANY = `msgid ""
msgstr ""
"Language: es-ES\\n"

msgid "Zebra"
msgstr "Cebra"

msgid "Apple"
msgstr "Manzana"

msgid "Mango"
msgstr "Mango"
`

test('compiles its messages in a settled order', () => {
	expect(Object.keys(compileCatalog(MANY))).toEqual(['', 'Apple', 'Mango', 'Zebra'])
})

test('serializes its messages in a settled order', () => {
	const held = serializeCatalog(compileCatalog(MANY))

	expect(held.indexOf('"Apple"')).toBeLessThan(held.indexOf('"Mango"'))
	expect(held.indexOf('"Mango"')).toBeLessThan(held.indexOf('"Zebra"'))
})

test('serializes a catalogue with its metadata entry first and a trailing newline', () => {
	const held = serializeCatalog(compileCatalog(CATALOGUE))

	expect(held.startsWith('{"":')).toBe(true)
	expect(held.endsWith('}\n')).toBe(true)
})

const TEMPLATE = `msgid ""
msgstr ""

msgid "Older posts"
msgstr ""

msgid "Newer posts"
msgstr ""
`

test('names every message still waiting for a translation', () => {
	expect(untranslated(CATALOGUE, TEMPLATE)).toEqual(['Newer posts'])
})

test('names every message the template no longer carries', () => {
	const stale = `${CATALOGUE}
msgid "Retired"
msgstr "Retirado"
`

	expect(orphaned(stale, TEMPLATE)).toEqual(['Retired'])
})

test('names a translation whose placeholders its message does not name', () => {
	const naming = 'msgid "%(count)d post"\nmsgstr ""\n'
	const broken = 'msgid "%(count)d post"\nmsgstr "%(total)d entrada"\n'

	expect(mismatched(broken, naming)).toEqual(['%(count)d post'])
})

test('passes a translation naming the placeholders its message names', () => {
	const naming = 'msgid "%(count)d post"\nmsgstr ""\n'
	const answered = 'msgid "%(count)d post"\nmsgstr "%(count)d entrada"\n'

	expect(mismatched(answered, naming)).toEqual([])
})

test('names a translation carrying a placeholder that names nothing', () => {
	const naming = 'msgid "%(count)d post"\nmsgstr ""\n'
	const bare = 'msgid "%(count)d post"\nmsgstr "%d entrada"\n'

	expect(mismatched(bare, naming)).toEqual(['%(count)d post'])
})

test('passes a translation keeping the bare placeholder its message names', () => {
	const naming = 'msgid "Disable %s"\nmsgstr ""\n'
	const answered = 'msgid "Disable %s"\nmsgstr "Desactivar a %s"\n'

	expect(mismatched(answered, naming)).toEqual([])
})

test('names a translation dropping the bare placeholder its message names', () => {
	const naming = 'msgid "Disable %s"\nmsgstr ""\n'
	const dropped = 'msgid "Disable %s"\nmsgstr "Desactivar"\n'

	expect(mismatched(dropped, naming)).toEqual(['Disable %s'])
})

test('passes a translation reordering the positional placeholders its message names', () => {
	const naming = 'msgid "%1$s of %2$s"\nmsgstr ""\n'
	const answered = 'msgid "%1$s of %2$s"\nmsgstr "%2$s de %1$s"\n'

	expect(mismatched(answered, naming)).toEqual([])
})

test('names a message that shares its name with a prototype member', () => {
	const naming = 'msgid "Older posts"\nmsgstr ""\n'
	const stale = 'msgid ""\nmsgstr ""\n\nmsgid "constructor"\nmsgstr "constructor"\n'

	expect(orphaned(stale, naming)).toEqual(['constructor'])
})

test('reads a template naming a prototype member the catalogue does not answer', () => {
	const naming = 'msgid ""\nmsgstr ""\n\nmsgid "toString"\nmsgstr ""\n'
	const held = 'msgid ""\nmsgstr ""\n'

	expect(() => mismatched(held, naming)).not.toThrow()
	expect(mismatched(held, naming)).toEqual([])
})

test('serializes a catalogue carrying no metadata entry as readable JSON', () => {
	const held = serializeCatalog({ Saved: ['Guardado'] })

	expect(() => JSON.parse(held)).not.toThrow()
})

test('names a translation answering a named placeholder with a bare one', () => {
	const naming = 'msgid "%(field)s declared."\nmsgstr ""\n'
	const swapped = 'msgid "%(field)s declared."\nmsgstr "%s declarado."\n'

	expect(mismatched(swapped, naming)).toEqual(['%(field)s declared.'])
})

test('passes a message no translation answers yet', () => {
	const naming = 'msgid "%(count)d post"\nmsgstr ""\n'
	const waiting = 'msgid "%(count)d post"\nmsgstr ""\n'

	expect(mismatched(waiting, naming)).toEqual([])
})

test('reads each plural form against the form it answers', () => {
	const naming = `msgid "%(count)d post"
msgid_plural "%(count)d posts"
msgstr[0] ""
msgstr[1] ""
`
	const broken = `msgid "%(count)d post"
msgid_plural "%(count)d posts"
msgstr[0] "%(count)d entrada"
msgstr[1] "%(total)d entradas"
`

	expect(mismatched(broken, naming)).toEqual(['%(count)d post'])
})

test('passes over the metadata entry rather than reading it as a message', () => {
	const naming = 'msgid ""\nmsgstr ""\n\nmsgid "Older posts"\nmsgstr ""\n'

	expect(mismatched(CATALOGUE, naming)).toEqual([])
})

/** REVIEWING is a catalogue holding one settled answer and two fuzzy ones. */
const REVIEWING = `msgid ""
msgstr ""

msgid "Older posts"
msgstr "Entradas anteriores"

#, fuzzy
msgid "Newer posts"
msgstr "Entradas nuevas"

#, fuzzy
msgctxt "status"
msgid "Draft"
msgstr "Borrador"
`

test('keeps a fuzzy answer in the compiled catalogue', () => {
	const held = compileCatalog(REVIEWING)

	expect(held['Newer posts']).toEqual(['Entradas nuevas'])
})

test('counts a fuzzy answer as answered rather than waiting', () => {
	const naming = 'msgid "Newer posts"\nmsgstr ""\n'

	expect(untranslated(REVIEWING, naming)).toEqual([])
})

test('names every answer still waiting for review', () => {
	expect(unreviewed(REVIEWING)).toEqual(['Newer posts', 'statusDraft'])
})

test('does not read an empty fuzzy entry as waiting for review', () => {
	const empty = 'msgid "Older posts"\nmsgstr ""\n\n#, fuzzy\nmsgid "Newer posts"\nmsgstr ""\n'

	expect(unreviewed(empty)).toEqual([])
})

test('sees a fuzzy answer through the mismatched gate', () => {
	const naming = 'msgid "%(count)d post"\nmsgstr ""\n'
	const fuzzyBroken = '#, fuzzy\nmsgid "%(count)d post"\nmsgstr "%(total)d entrada"\n'

	expect(mismatched(fuzzyBroken, naming)).toEqual(['%(count)d post'])
})
