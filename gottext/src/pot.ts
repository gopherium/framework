// SPDX-License-Identifier: Apache-2.0

import { GettextExtractor, JsExtractors } from 'gettext-extractor'
import { po, type GetTextTranslation } from 'gettext-parser'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

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
	/** domain is the text domain the template carries. */
	domain: string
	/** root is the path the source globs resolve against. */
	root: string
	/** sources are the globs holding every message, read from the root. */
	sources: string[]
	/** ignored are the paths inside those globs that ship no message, read beside the baseline. */
	ignored?: string[]
	/** goRoots are the directories a Go walk reads, none by default. */
	goRoots?: string[]
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
 * Returns every translatable message the given sources declare.
 * @param options - Where to read the sources from.
 * @returns The messages the extractor found.
 */
export function messages(options: PotOptions): Found[] {
	const extractor = new GettextExtractor()
	const parser = extractor.createJsParser([
		JsExtractors.callExpression(['__', 'i18n.__'], {
			arguments: { text: 0 },
			comments: NO_COMMENTS,
		}),
		JsExtractors.callExpression(['_x', 'i18n._x'], {
			arguments: { text: 0, context: 1 },
			comments: NO_COMMENTS,
		}),
		JsExtractors.callExpression(['_n', 'i18n._n'], {
			arguments: { text: 0, textPlural: 1 },
			comments: NO_COMMENTS,
		}),
		JsExtractors.callExpression(['_nx', 'i18n._nx'], {
			arguments: { text: 0, textPlural: 1, context: 3 },
			comments: NO_COMMENTS,
		}),
	])
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
	const translations: Record<string, Record<string, GetTextTranslation>> = {}
	for (const text of goMessages(options.root, options.goRoots ?? [])) {
		translations[''] ??= {}
		translations[''][text] = { msgid: text, msgstr: [''] }
	}
	for (const message of messages(options)) {
		const context = message.context ?? ''
		translations[context] ??= {}
		translations[context][message.text] = entryOf(message)
	}
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
