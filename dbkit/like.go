// SPDX-License-Identifier: Apache-2.0

package dbkit

import "strings"

// likeEscaper puts a backslash before every LIKE pattern character and before the backslash itself.
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// EscapeLike returns term with every LIKE pattern character escaped by a backslash.
func EscapeLike(term string) string {
	return likeEscaper.Replace(term)
}
