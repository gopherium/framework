// SPDX-License-Identifier: Apache-2.0

import { sprintf } from '@wordpress/i18n'

import { held } from './catalog.js'

/** RefusedAnswer is what a server answered when it turned a request away. */
export interface RefusedAnswer {
	/** message is the server's own prose, empty when it said nothing readable. */
	message: string
	/** code is the stable name of the condition, absent when the server named none. */
	code?: string
	/** meta carries the named values the condition's message speaks about. */
	meta?: Record<string, unknown>
}

/** PLACEHOLDERS matches the named places a template asks the answer to fill in, as sprintf reads them. */
const PLACEHOLDERS =
	/%\(([$_a-zA-Z][$_a-zA-Z0-9]*)\)[ +0#-]*\d*(?:\.(?:\d+|\*))?(?:ll|[lhqL])?[cduxXefgsp]/g

/**
 * Reports whether the answer carries every value the template names.
 * @param template - The message to fill in.
 * @param meta - The data the answer carries.
 * @returns True when nothing the template asks for is missing.
 */
function filled(template: string, meta: Record<string, unknown>): boolean {
	for (const match of template.matchAll(PLACEHOLDERS)) {
		if (held(meta, match[1]) === undefined) {
			return false
		}
	}
	return true
}

/**
 * Returns the message a reader is shown for a refused request, in their own language.
 * @param refused - What the server answered.
 * @param templates - The translated message each code stands for.
 * @param fallback - The translated words to show when the answer says nothing readable.
 * @returns The message to show.
 */
export function errorText(
	refused: RefusedAnswer,
	templates: Record<string, string>,
	fallback: string,
): string {
	const spoken = refused.message === '' ? fallback : refused.message
	const template = refused.code === undefined ? undefined : held(templates, refused.code)
	const meta = refused.meta ?? {}
	if (template === undefined || !filled(template, meta)) {
		return spoken
	}
	return sprintf(template, meta as never)
}
