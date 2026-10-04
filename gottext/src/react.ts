// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from 'react'

import { rememberFormatLocale } from './format.js'

/**
 * Renders the screens once the settings named the format locale, or once reading them failed.
 * @param props - The format locale the settings named, whether reading them failed, the stand in and the screens.
 * @returns The screens, or the stand in while the settings load.
 */
export function FormatLocaleGate({ locale, failed, loading, children }: {
	locale: string | undefined
	failed: boolean
	loading: ReactNode
	children: ReactNode
}): ReactNode {
	if (locale !== undefined) {
		rememberFormatLocale(locale)
		return children
	}
	return failed ? children : loading
}
