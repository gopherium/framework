// SPDX-License-Identifier: Apache-2.0

import { GettextExtractor, JsExtractors } from 'gettext-extractor'
import { po, type GetTextTranslation } from 'gettext-parser'
import { globSync, readFileSync, readdirSync } from 'node:fs'
import { join, resolve } from 'node:path'

/** IGNORED is the set of paths that can never name a source, whatever a consumer ships. */
const IGNORED = ['**/node_modules/**', '**/*.d.ts', '**/*.test.ts', '**/*.test.tsx']

/** NO_COMMENTS is the comment positions the extractor leaves out of the template. */
const NO_COMMENTS = { otherLineLeading: false, sameLineLeading: false, sameLineTrailing: false }

/** SKIPPED_DIRS is the directories a source walk skips whole. */
const SKIPPED_DIRS = new Set(['node_modules', 'dist', 'coverage'])

/** TEMPLATE_CALL is the shape a Go template asks the translator with. */
const TEMPLATE_CALL = /\bT\.Get "((?:[^"\\]|\\.)*)"/g

/** MARKER_CALL is the shape Go source marks a message with. */
const MARKER_CALL = /\bMsgid\("((?:[^"\\]|\\.)*)"\)/g

/** DEFAULT_DOMAIN is the text domain a call naming none belongs to. */
const DEFAULT_DOMAIN = 'default'

/** MANIFEST_TEXTS is the keys of a plugin manifest whose text the template carries. */
const MANIFEST_TEXTS = ['name', 'description']

/** SETTING_TEXTS is the keys of each setting a plugin manifest declares whose text the template carries. */
const SETTING_TEXTS = ['label', 'help']

/** Entry is one JSON object a plugin manifest holds, read key by key. */
type Entry = Record<string, unknown>

/** Manifest is one plugin manifest read whole. */
interface Manifest {
	/** path is where the manifest sits, as its glob names it, and the reference its texts carry. */
	path: string
	/** entry is the top object the manifest holds. */
	entry: Entry
}

/** Translations is every entry of a template, by context then message. */
type Translations = Record<string, Record<string, GetTextTranslation>>

/** Extractor is what the parser runs over every piece of a source. */
type Extractor = ReturnType<typeof JsExtractors.callExpression>

/** Piece is one part of a parsed source the parser hands an extractor. */
type Piece = Parameters<Extractor>[0]

/** Parsed is the parsed source a piece sits in. */
type Parsed = Parameters<Extractor>[1]

/** Call is the part of a call the text domain check reads. */
interface Call {
	/** arguments are the values the call passes. */
	arguments: ArrayLike<unknown>
}

/** Shape is one gettext call shape and where each of its arguments stands. */
interface Shape {
	/** callees are the names the call goes by. */
	callees: string[]
	/** positions are where the text, the plural and the context stand. */
	positions: { text: number, textPlural?: number, context?: number }
	/** domain is where the text domain stands. */
	domain: number
}

/** SHAPES is the four gettext call shapes, placed as the WordPress packages place them. */
const SHAPES: Shape[] = [
	{ callees: ['__', 'i18n.__'], positions: { text: 0 }, domain: 1 },
	{ callees: ['_x', 'i18n._x'], positions: { text: 0, context: 1 }, domain: 2 },
	{ callees: ['_n', 'i18n._n'], positions: { text: 0, textPlural: 1 }, domain: 3 },
	{ callees: ['_nx', 'i18n._nx'], positions: { text: 0, textPlural: 1, context: 3 }, domain: 4 },
]

/** Found is a message one of the four gettext calls declared. */
export interface Found {
	/** text is the message as the sources write it. */
	text: string
	/** textPlural is the plural form, absent unless the call named one. */
	textPlural: string | null
	/** context is what tells two senses of one word apart, absent unless the call named one. */
	context: string | null
}

/** PotOptions is what building a template needs. */
export interface PotOptions {
	/** domain is the text domain the template carries, and the only one whose calls it keeps from the sources. */
	domain: string
	/** root is the path the source globs resolve against. */
	root: string
	/** sources are the globs holding every message, read from the root. */
	sources: string[]
	/** ignored are the paths inside those globs that ship no message, read beside the baseline. */
	ignored?: string[]
	/** goRoots are the directories a Go walk reads, none by default. */
	goRoots?: string[]
	/** manifests are the globs naming the plugin manifests to read, from the root, none by default. */
	manifests?: string[]
}

/**
 * Returns every file under a directory, skipping installed and built trees.
 * @param dir - The directory to walk.
 * @returns The files found, as absolute paths.
 */
function sourceFiles(dir: string): string[] {
	const held: string[] = []
	for (const entry of readdirSync(dir, { withFileTypes: true })) {
		if (entry.isDirectory()) {
			if (!SKIPPED_DIRS.has(entry.name)) {
				held.push(...sourceFiles(join(dir, entry.name)))
			}
			continue
		}
		held.push(join(dir, entry.name))
	}
	return held
}

/**
 * Returns the message a captured Go quoted string holds.
 * @param raw - The captured text between the quotes.
 * @param file - The file the capture came from.
 * @returns The message with its escapes resolved.
 */
export function goString(raw: string, file: string): string {
	try {
		return JSON.parse(`"${raw}"`) as string
	} catch {
		throw new Error(`the string ${raw} in ${file} holds an escape this extractor cannot read`)
	}
}

/**
 * Adds the messages one file declares to the given set.
 * @param file - The file to read.
 * @param held - The set the messages land in.
 */
function collect(file: string, held: Set<string>): void {
	if (file.endsWith('.html')) {
		for (const found of readFileSync(file, 'utf8').matchAll(TEMPLATE_CALL)) {
			held.add(goString(found[1], file))
		}
		return
	}
	if (!file.endsWith('.go') || file.endsWith('_test.go')) {
		return
	}
	for (const found of readFileSync(file, 'utf8').matchAll(MARKER_CALL)) {
		held.add(goString(found[1], file))
	}
}

/**
 * Returns every message the Go side declares, through its markers and its templates.
 * @param root - The path the walk starts from.
 * @param dirs - The directories to walk.
 * @returns The messages found.
 */
export function goMessages(root: string, dirs: string[]): string[] {
	const held = new Set<string>()
	for (const dir of dirs) {
		for (const file of sourceFiles(join(root, dir))) {
			collect(file, held)
		}
	}
	return [...held]
}

/**
 * Returns whether a JSON value is an object, rather than a list, a text, a number or null.
 * @param value - The value to check.
 * @returns True when the value is an object.
 */
function isEntry(value: unknown): value is Entry {
	return value instanceof Object && !Array.isArray(value)
}

/**
 * Returns the text one key of a manifest object holds, none when the key is absent, null or empty.
 * @param manifest - The manifest the object sits in, named in an error.
 * @param entry - The object to read.
 * @param key - The key to read.
 * @returns The text, or nothing.
 */
function textOf(manifest: Manifest, entry: Entry, key: string): string[] {
	const text = entry[key] ?? ''
	if (typeof text !== 'string') {
		throw new Error(`the manifest ${manifest.path} holds a ${key} that is not text`)
	}
	return text === '' ? [] : [text]
}

/**
 * Returns the settings a manifest declares, none when it names none.
 * @param manifest - The manifest to read.
 * @returns The declared settings.
 */
function settingsOf(manifest: Manifest): Entry[] {
	const settings = manifest.entry.settings ?? []
	if (!Array.isArray(settings) || !settings.every(isEntry)) {
		throw new Error(`the manifest ${manifest.path} holds settings that are not a list of objects`)
	}
	return settings
}

/**
 * Returns the plugin manifest at one path, read whole.
 * @param options - The root the path resolves against.
 * @param path - The manifest's path, as its glob names it.
 * @returns The manifest with its top object.
 */
function manifestAt(options: PotOptions, path: string): Manifest {
	let body: string
	try {
		body = readFileSync(resolve(options.root, path), 'utf8')
	} catch (cause) {
		throw new Error(`the manifest ${path} cannot be read`, { cause })
	}
	let entry: unknown
	try {
		entry = JSON.parse(body)
	} catch {
		throw new Error(`the manifest ${path} holds JSON this extractor cannot read`)
	}
	if (!isEntry(entry)) {
		throw new Error(`the manifest ${path} holds no JSON object`)
	}
	return { path, entry }
}

/**
 * Returns the texts one plugin manifest hands the template.
 * @param manifest - The manifest to read.
 * @returns The plugin's name and description, then each setting's label and help.
 */
function manifestTexts(manifest: Manifest): string[] {
	return [
		...MANIFEST_TEXTS.flatMap((key) => textOf(manifest, manifest.entry, key)),
		...settingsOf(manifest).flatMap((setting) => SETTING_TEXTS.flatMap((key) => textOf(manifest, setting, key))),
	]
}

/**
 * Returns the paths the manifest globs match, rejecting a glob that matches none.
 * @param options - The root and the manifest globs to read.
 * @returns The paths found, as the globs name them, in a settled order.
 */
function manifestPaths(options: PotOptions): string[] {
	return (options.manifests ?? []).flatMap((pattern) => {
		const found = globSync(pattern, { cwd: options.root })
		if (found.length === 0) {
			throw new Error(`no manifest matches ${pattern}`)
		}
		return found
	}).sort()
}

/**
 * Returns each text the plugin manifests hand the template, with the manifests holding it.
 * @param options - The root and the manifest globs to read.
 * @returns The texts found, each with the paths of its manifests in a settled order.
 */
function manifestMessages(options: PotOptions): Map<string, Set<string>> {
	const held = new Map<string, Set<string>>()
	for (const path of manifestPaths(options)) {
		for (const text of manifestTexts(manifestAt(options, path))) {
			held.set(text, (held.get(text) ?? new Set<string>()).add(path))
		}
	}
	return held
}

/**
 * Adds each text the plugin manifests hold to the template, naming its manifests as its reference.
 * @param translations - The template entries, by context then message.
 * @param options - The root and the manifest globs to read.
 */
function addManifests(translations: Translations, options: PotOptions): void {
	for (const [text, paths] of manifestMessages(options)) {
		translations[''] ??= {}
		translations[''][text] ??= { msgid: text, msgstr: [''] }
		translations[''][text].comments = { reference: [...paths].join('\n') }
	}
}

/**
 * Returns the key an entry is ordered by.
 * @param entry - The entry to key.
 * @returns The context and message joined.
 */
function keyOf(entry: GetTextTranslation): string {
	return `${entry.msgctxt ?? ''} ${entry.msgid}`
}

/**
 * Returns the ordering placing entries by context then message, by code unit.
 * @param left - The entry on the left.
 * @param right - The entry on the right.
 * @returns A negative number when the left entry sorts first.
 */
function byKey(left: GetTextTranslation, right: GetTextTranslation): number {
	return Number(keyOf(left) > keyOf(right)) - Number(keyOf(left) < keyOf(right))
}

/**
 * Returns the check telling whether a call of one shape names the given text domain.
 * @param shape - The call shape to read.
 * @param domain - The text domain the template is built for.
 * @returns The check, which keeps a call whose text domain is not written as a string.
 */
function namesDomain(shape: Shape, domain: string): (call: Piece, file: Parsed, start: number) => boolean {
	const named = JsExtractors.callExpression(shape.callees, {
		arguments: { text: shape.domain },
		comments: NO_COMMENTS,
	})
	return (call, file, start) => {
		if ((call as unknown as Call).arguments.length <= shape.domain) {
			return domain === DEFAULT_DOMAIN
		}
		const names: string[] = []
		named(call, file, (found) => { names.push(found.text) }, start)
		return names.length === 0 || names[0] === domain
	}
}

/**
 * Returns the extractor keeping the calls of one shape that name the given text domain.
 * @param shape - The call shape to read.
 * @param domain - The text domain the template is built for.
 * @returns The extractor the parser runs over every piece of a source.
 */
function extractorFor(shape: Shape, domain: string): Extractor {
	const message = JsExtractors.callExpression(shape.callees, {
		arguments: shape.positions,
		comments: NO_COMMENTS,
	})
	const keeps = namesDomain(shape, domain)
	return (piece, file, add, start) => {
		message(piece, file, (found) => {
			if (keeps(piece, file, start)) {
				add(found)
			}
		}, start)
	}
}

/**
 * Returns every translatable message the given sources declare under the given text domain.
 * @param options - Where to read the sources from and the text domain to keep.
 * @returns The messages the extractor found.
 */
export function messages(options: PotOptions): Found[] {
	const extractor = new GettextExtractor()
	const parser = extractor.createJsParser(SHAPES.map((shape) => extractorFor(shape, options.domain)))
	for (const pattern of options.sources) {
		parser.parseFilesGlob(pattern, {
			cwd: options.root,
			ignore: [...IGNORED, ...(options.ignored ?? [])],
			absolute: true,
		})
	}
	return extractor.getMessages() as Found[]
}

/**
 * Returns the template entry one found message becomes.
 * @param message - The message the extractor found.
 * @returns The entry the template carries for it.
 */
function entryOf(message: Found): GetTextTranslation {
	return {
		msgctxt: message.context ?? undefined,
		msgid: message.text,
		msgid_plural: message.textPlural ?? undefined,
		msgstr: message.textPlural ? ['', ''] : [''],
	}
}

/**
 * Returns the catalogue template built from the given sources.
 * @param options - The domain to carry and where to read the sources from.
 * @returns The template as UTF-8 bytes.
 */
export function pot(options: PotOptions): Buffer {
	const translations: Translations = {}
	for (const text of goMessages(options.root, options.goRoots ?? [])) {
		translations[''] ??= {}
		translations[''][text] = { msgid: text, msgstr: [''] }
	}
	for (const message of messages(options)) {
		const context = message.context ?? ''
		translations[context] ??= {}
		translations[context][message.text] = entryOf(message)
	}
	addManifests(translations, options)
	return po.compile(
		{
			charset: 'UTF-8',
			headers: {
				'Project-Id-Version': options.domain,
				'MIME-Version': '1.0',
				'Content-Type': 'text/plain; charset=UTF-8',
				'Content-Transfer-Encoding': '8bit',
				'Plural-Forms': 'nplurals=2; plural=(n != 1);',
				'X-Domain': options.domain,
			},
			translations,
		},
		{ sort: byKey, foldLength: 0, eol: '\n' },
	)
}
