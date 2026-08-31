// SPDX-License-Identifier: Apache-2.0

import { localeOf, namedMessages } from './merge.js'

/** REQUEST_TIMEOUT is how long any one call to the platform may take. */
const REQUEST_TIMEOUT = 30_000

/** API is where the translation platform answers. */
const API = 'https://api.poeditor.com/v2'

/** Answer is what the platform answers a request with. */
interface Answer {
	response: { status: string, code?: string, message?: string }
	result?: { languages?: { code: string }[], url?: string, terms?: { deleted?: number } }
}

/** RATE_LIMITED is the refusal code the platform answers hurried uploads with. */
const RATE_LIMITED = '4048'

/** Poeditor is what a repository reads a translation platform through. */
export interface Poeditor {
	languages: () => Promise<string[]>
	exportPo: (locale: string) => Promise<string>
	uploadTerms: (source: string) => Promise<void>
	uploadTranslations: (locale: string, source: string) => Promise<void>
	addLanguage: (locale: string) => Promise<void>
}

/** Retiring is what a repository retires a platform's absent terms through. */
export interface Retiring {
	retireTerms: (source: string) => Promise<number>
}

/** PlatformOptions is what reaching one translation platform project needs. */
export interface PlatformOptions {
	/** token is the credential the platform answers to. */
	token: string
	/** project is the project the translations live in. */
	project: string
	/** domain is the text domain, which names the template the upload carries. */
	domain: string
	/** fetched is how a request is sent, the runtime's own by default. */
	fetched?: typeof fetch
	/** paced is how many milliseconds separate uploads and precede a rate retry. */
	paced?: number
	/** paused is how a wait is spent, the runtime's own timer by default. */
	paused?: (ms: number) => Promise<void>
}

/**
 * Returns the platform's answer, refusing a response that never arrived whole.
 * @param response - The answer as it arrived.
 * @returns The answer, parsed.
 */
async function answerOf(response: Response): Promise<Answer> {
	if (!response.ok) {
		throw new Error(`the translation platform answered ${response.status}`)
	}
	return (await response.json()) as Answer
}

/**
 * Returns the result an answer carries, refusing anything that is not a success.
 * @param answered - The answer, parsed.
 * @returns The result the platform answered with.
 */
function resultChecked(answered: Answer): NonNullable<Answer['result']> {
	if (answered.response.status !== 'success') {
		throw new Error(
			`the translation platform refused: ${answered.response.message ?? 'no reason given'}`,
		)
	}
	return answered.result ?? {}
}

/**
 * Returns the result a platform answer carries, refusing anything that is not a success.
 * @param response - The answer as it arrived.
 * @returns The result the platform answered with.
 */
async function resultOf(response: Response): Promise<NonNullable<Answer['result']>> {
	return resultChecked(await answerOf(response))
}

/**
 * Returns the reader and retirer of one translation platform project.
 * @param options - The credential, the project and the domain to reach it under.
 * @returns The reader, carrying the retirement its own interface names.
 */
export function poeditorAt(options: PlatformOptions): Poeditor & Retiring {
	const fetched = options.fetched ?? fetch
	const paced = options.paced ?? 20_000
	const paused = options.paused
		?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)))
	let uploaded = false
	/**
	 * Returns the values every call carries.
	 * @returns The credential and the project.
	 */
	function credentials(): URLSearchParams {
		return new URLSearchParams({ api_token: options.token, id: options.project })
	}
	/**
	 * Returns the platform's answer to one call.
	 * @param path - The call to make.
	 * @param form - The values the call carries.
	 * @returns The result the platform answered with.
	 */
	async function ask(
		path: string,
		form: URLSearchParams,
	): Promise<NonNullable<Answer['result']>> {
		return resultOf(await fetched(`${API}/${path}`, {
			method: 'POST',
			body: form,
			signal: AbortSignal.timeout(REQUEST_TIMEOUT),
		}))
	}
	/**
	 * Returns an upload form carrying the credentials and the named file.
	 * @param updating - What the upload changes on the platform.
	 * @param filename - The name the file travels under.
	 * @param source - The file's text.
	 * @returns The form, ready for the extras one upload adds.
	 */
	function formFor(updating: string, filename: string, source: string): FormData {
		const form = new FormData()
		form.set('api_token', options.token)
		form.set('id', options.project)
		form.set('updating', updating)
		form.set('file', new Blob([source]), filename)
		return form
	}
	/**
	 * Sends one upload form, pacing it behind the last and retrying one rate refusal.
	 * @param form - The upload to send.
	 * @returns The result the platform answered with.
	 */
	async function uploadForm(form: FormData): Promise<NonNullable<Answer['result']>> {
		if (uploaded) {
			await paused(paced)
		}
		uploaded = true
		const send = () => fetched(`${API}/projects/upload`, {
			method: 'POST',
			body: form,
			signal: AbortSignal.timeout(REQUEST_TIMEOUT),
		})
		const first = await answerOf(await send())
		if (first.response.code !== RATE_LIMITED) {
			return resultChecked(first)
		}
		await paused(paced)
		return resultOf(await send())
	}
	/**
	 * Sends the template to the platform, saying whether absent terms retire.
	 * @param source - The catalogue template as POT text.
	 * @param retiring - Whether terms the template does not name are deleted.
	 * @returns The result the platform answered with.
	 */
	async function sendTemplate(
		source: string,
		retiring: boolean,
	): Promise<NonNullable<Answer['result']>> {
		const form = formFor('terms', `${options.domain}.pot`, source)
		if (retiring) {
			form.set('sync_terms', '1')
		}
		return uploadForm(form)
	}
	return {
		/**
		 * Returns every language the project carries.
		 * @returns The languages, named as a catalogue file names them.
		 */
		languages: async () => {
			const held = await ask('languages/list', credentials())
			return (held.languages ?? []).map((named) => localeOf(named.code))
		},
		/**
		 * Adds to the platform every term the template names, leaving translations alone.
		 * @param source - The catalogue template as POT text.
		 */
		uploadTerms: async (source: string) => {
			await sendTemplate(source, false)
		},
		/**
		 * Tells the platform a language exists, so a catalogue can follow.
		 * @param locale - The language to add.
		 */
		addLanguage: async (locale: string) => {
			const form = credentials()
			form.set('language', locale.toLowerCase())
			await ask('languages/add', form)
		},
		/**
		 * Sends one language's terms and translations together, fuzzy flags preserved.
		 * @param locale - The language, named as the platform names it.
		 * @param source - The catalogue as PO text.
		 */
		uploadTranslations: async (locale: string, source: string) => {
			const form = formFor('terms_translations', `${options.domain}.po`, source)
			form.set('overwrite', '1')
			form.set('language', locale.toLowerCase())
			await uploadForm(form)
		},
		/**
		 * Deletes from the platform every term the template does not name.
		 * @param source - The catalogue template as POT text.
		 * @returns How many terms the platform deleted.
		 */
		retireTerms: async (source: string) => {
			if (namedMessages(source) === 0) {
				throw new Error('the template names no messages, so retiring would empty the project')
			}
			return (await sendTemplate(source, true)).terms?.deleted ?? 0
		},
		/**
		 * Returns one language's catalogue as the platform exports it.
		 * @param locale - The language to export.
		 * @returns The catalogue as PO text.
		 */
		exportPo: async (locale: string) => {
			const form = credentials()
			form.set('language', locale.toLowerCase())
			form.set('type', 'po')
			const held = await ask('projects/export', form)
			if (held.url === undefined) {
				throw new Error(`the translation platform named no export for ${locale}`)
			}
			const downloaded = await fetched(held.url, {
				signal: AbortSignal.timeout(REQUEST_TIMEOUT),
			})
			if (!downloaded.ok) {
				throw new Error(`the export for ${locale} answered ${downloaded.status}`)
			}
			return downloaded.text()
		},
	}
}
