// SPDX-License-Identifier: Apache-2.0

/** Catalog is one compiled gettext catalogue as setLocaleData consumes it. */
export type Catalog = Record<string, string[] | Record<string, string>>

/** METADATA is the entry carrying a catalogue's headers rather than a message. */
export const METADATA = ''

/** Chunks are the lazy catalogue modules a bundler produced, keyed by path. */
export type Chunks = Record<string, () => Promise<{ default: Catalog }>>

/**
 * Returns the file name a path ends in, without its extension.
 * @param path - The path a bundler keyed a chunk under.
 * @returns The stem naming the locale.
 */
function stemOf(path: string): string {
	const name = path.slice(path.lastIndexOf('/') + 1)
	const dot = name.lastIndexOf('.')
	return dot === -1 ? name : name.slice(0, dot)
}

/**
 * Returns the loader answering the catalogue a locale names among the chunks.
 * @param chunks - The lazy catalogue modules a bundler produced.
 * @returns A loader answering one locale's catalogue, or nothing when none ships.
 */
export function globCatalogs(chunks: Chunks): (locale: string) => Promise<Catalog | undefined> {
	const byLocale = new Map<string, () => Promise<{ default: Catalog }>>()
	for (const [path, load] of Object.entries(chunks)) {
		byLocale.set(stemOf(path), load)
	}
	return async (locale: string): Promise<Catalog | undefined> => {
		const load = byLocale.get(locale)
		return load === undefined ? undefined : (await load()).default
	}
}

/**
 * Returns the declared plural count, or two when the catalogue has no plural rule.
 * @param rule - The catalogue's plural rule, if it declares one.
 * @returns How many forms a plural message requires.
 * @throws When a declared rule names no usable count.
 */
export function pluralCount(rule: string | undefined): number {
	if (rule === undefined) {
		return 2
	}
	const counted = /nplurals\s*=\s*(\d+)/.exec(rule)
	const forms = counted === null ? 0 : Number(counted[1])
	if (forms < 1) {
		throw new Error('the committed catalogue declares a plural rule naming no usable count')
	}
	return forms
}

/**
 * Returns the entry a table holds under a key, ignoring anything it inherits.
 * @param table - The table to read, or nothing when the catalogue lacks it.
 * @param key - The key to look under.
 * @returns The entry, or nothing when the table holds none of its own.
 */
export function held<T>(table: Record<string, T> | undefined, key: string): T | undefined {
	return table !== undefined && Object.hasOwn(table, key) ? table[key] : undefined
}

/**
 * Returns the key a message waits under, its context and message joined.
 * @param context - The context telling two senses of one word apart, if any.
 * @param msgid - The source message.
 * @returns The lookup key.
 */
export function keyOf(context: string, msgid: string): string {
	return context === '' ? msgid : `${context}${msgid}`
}
