// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/gopherium/framework/dbkit"
)

var (
	// errBareAt refuses a URL address whose user or password holds a bare @.
	errBareAt = fmt.Errorf("%w: the user or password holds a bare @, write each one as %%40", dbkit.ErrAddress)
	// errQueryAt refuses a URL address with a ? or # before an @ ahead of its first /.
	errQueryAt = fmt.Errorf("%w: a ? or # comes before an @ ahead of the first /, "+
		"write ? as %%3F, # as %%23 and @ as %%40", dbkit.ErrAddress)
	// errUserCharacter refuses a URL address whose user or password holds a character pgx 5.10 refuses.
	errUserCharacter = fmt.Errorf("%w: the user or password holds a character pgx 5.10 cannot read, "+
		"write it percent-encoded", dbkit.ErrAddress)
	// errHash refuses a URL address that holds a raw #.
	errHash = fmt.Errorf("%w: the address holds a #, write it as %%23", dbkit.ErrAddress)
	// errSpace refuses a URL address that holds a raw space or control character.
	errSpace = fmt.Errorf("%w: the address holds a space or a control character, write a space as %%20",
		dbkit.ErrAddress)
	// errEscape refuses a URL address with a % that starts no escape or escapes a NUL byte.
	errEscape = fmt.Errorf("%w: a %% starts no escape or escapes a NUL byte, write a %% as %%25", dbkit.ErrAddress)
	// errEmptyHost refuses an address whose host list holds an empty host.
	errEmptyHost = fmt.Errorf("%w: the host list holds an empty host, name every host", dbkit.ErrAddress)
	// errMixedPorts refuses a URL address whose host list gives a port to some hosts only.
	errMixedPorts = fmt.Errorf("%w: some hosts of the host list carry a port and others do not, "+
		"give every host a port or none", dbkit.ErrAddress)
	// errSlashDatabase refuses a URL address whose path gives a database name that starts with a /.
	errSlashDatabase = fmt.Errorf("%w: the database name starts with a /, give it as ?dbname= with the / written as %%2F",
		dbkit.ErrAddress)
	// errQueryPlus refuses a URL address whose query holds a raw +.
	errQueryPlus = fmt.Errorf("%w: the query holds a +, write it as %%2B, or a space as %%20", dbkit.ErrAddress)
	// errQuerySemicolon refuses a URL address whose query holds a raw semicolon.
	errQuerySemicolon = fmt.Errorf("%w: the query holds a semicolon, write it as %%3B", dbkit.ErrAddress)
	// errQueryPair refuses a URL address with a query pair of no = or more than one.
	errQueryPair = fmt.Errorf("%w: a query pair lacks its = or holds a second one, write a = inside a value as %%3D",
		dbkit.ErrAddress)
	// errQuerySSL refuses a URL address whose query sets ssl.
	errQuerySSL = fmt.Errorf("%w: the query sets ssl, set sslmode instead", dbkit.ErrAddress)
	// errQueryRepeat refuses a URL address whose query sets one key twice, dbname and database being one key.
	errQueryRepeat = fmt.Errorf("%w: the query sets a key twice, set each key once, with dbname and database as one key",
		dbkit.ErrAddress)
	// errBackslash refuses a keyword and value address that holds a backslash.
	errBackslash = fmt.Errorf("%w: the keyword and value address holds a backslash, "+
		"write the address as a URL with %%5C", dbkit.ErrAddress)
	// errEmpty refuses an empty or blank address.
	errEmpty = fmt.Errorf("%w: the address is empty or blank, "+
		"and pgx would read every setting from the PG* environment variables", dbkit.ErrAddress)
)

// userinfoCharacters are the raw characters pgx 5.10 reads in the user and password of a URL address.
const userinfoCharacters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._:~!$&'()*+,;=%"

// keywordSpace are the characters pgx reads as space between the pairs of a keyword and value address.
const keywordSpace = " \t\n\r\v\f"

// CheckAddress returns the error for an empty PostgreSQL address or one pgx 5.10 and 5.11 read apart with no PG* set.
func CheckAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return errEmpty
	}
	rest, isURL := cutScheme(address)
	if !isURL {
		return checkKeywordValue(address)
	}
	return checkURL(rest)
}

// checkKeywordValue returns the error for the first shape pgx 5.10 and 5.11 read apart in a keyword and value address.
func checkKeywordValue(address string) error {
	if strings.ContainsRune(address, '\\') {
		return errBackslash
	}
	rest := strings.TrimLeft(address, keywordSpace)
	for rest != "" {
		var key, value string
		key, value, rest = nextKeywordPair(rest)
		if key == "host" && hasEmptyHost(value) {
			return errEmptyHost
		}
	}
	return nil
}

// nextKeywordPair returns the first key and value of rest, a keyword and value address with no backslash, and the rest.
func nextKeywordPair(rest string) (key, value, after string) {
	key, rest, found := strings.Cut(rest, "=")
	if !found {
		return "", "", ""
	}
	rest = strings.TrimLeft(rest, keywordSpace)
	if quoted, ok := strings.CutPrefix(rest, "'"); ok {
		value, after, _ = strings.Cut(quoted, "'")
	} else {
		value, after = cutAtSpace(rest)
	}
	return strings.Trim(key, keywordSpace), value, strings.TrimLeft(after, keywordSpace)
}

// cutAtSpace returns the text of s before its first keyword space and the text from there on.
func cutAtSpace(s string) (before, from string) {
	end := strings.IndexAny(s, keywordSpace)
	if end < 0 {
		return s, ""
	}
	return s[:end], s[end:]
}

// cutScheme returns the part of a URL address after its :// and true, and false for any other address.
func cutScheme(address string) (string, bool) {
	rest, ok := strings.CutPrefix(address, "postgres://")
	if !ok {
		rest, ok = strings.CutPrefix(address, "postgresql://")
	}
	return rest, ok
}

// checkURL returns the error for the first shape pgx 5.10 and 5.11 read apart in rest, a URL after its ://.
func checkURL(rest string) error {
	if err := checkAt(rest); err != nil {
		return err
	}
	if err := checkCharacters(rest); err != nil {
		return err
	}
	authority, after := splitAuthority(rest)
	if err := checkAuthority(authority); err != nil {
		return err
	}
	if path, ok := strings.CutPrefix(after, "/"); ok && startsWithSlash(path) {
		return errSlashDatabase
	}
	_, query, _ := strings.Cut(rest, "?")
	return checkQuery(query)
}

// checkCharacters returns the error for the first raw character or escape of rest that pgx 5.10 and 5.11 read apart.
func checkCharacters(rest string) error {
	switch {
	case strings.ContainsRune(rest, '#'):
		return errHash
	case strings.ContainsFunc(rest, isSpaceOrControl):
		return errSpace
	case strings.Contains(rest, "%00"):
		return errEscape
	}
	if _, err := url.PathUnescape(rest); err != nil {
		return errEscape
	}
	return nil
}

// isSpaceOrControl reports whether r is a space or an ASCII control character.
func isSpaceOrControl(r rune) bool {
	return r <= ' ' || r == '\x7f'
}

// splitAuthority returns the part of rest, a URL address after its ://, before its first / or ?, and the rest.
func splitAuthority(rest string) (authority, after string) {
	end := strings.IndexAny(rest, "/?")
	if end < 0 {
		return rest, ""
	}
	return rest[:end], rest[end:]
}

// checkAuthority returns the error for the user, password or hosts of authority that pgx 5.10 and 5.11 read apart.
func checkAuthority(authority string) error {
	at := strings.LastIndexByte(authority, '@')
	if at >= 0 && strings.ContainsFunc(authority[:at], outsideUserinfo) {
		return errUserCharacter
	}
	return checkHosts(authority[at+1:])
}

// outsideUserinfo reports whether pgx 5.10 refuses r as a raw character of a user or password.
func outsideUserinfo(r rune) bool {
	return !strings.ContainsRune(userinfoCharacters, r)
}

// startsWithSlash reports whether path, the text after the first / of a URL path, starts with a raw or escaped /.
func startsWithSlash(path string) bool {
	return strings.HasPrefix(path, "/") || strings.HasPrefix(strings.ToUpper(path), "%2F")
}

// checkHosts returns the error for a comma-separated host list that pgx 5.10 and 5.11 read apart.
func checkHosts(list string) error {
	hosts := strings.Split(list, ",")
	withPort := 0
	for _, host := range hosts {
		name, port := splitHost(host)
		if name == "" && len(hosts) > 1 {
			return errEmptyHost
		}
		if port != "" {
			withPort++
		}
	}
	if withPort > 0 && withPort < len(hosts) {
		return errMixedPorts
	}
	return nil
}

// splitHost returns the name and the port of host, one host of a URL host list, either of them empty when absent.
func splitHost(host string) (name, port string) {
	if bracketed, ok := strings.CutPrefix(host, "["); ok {
		name, rest, _ := strings.Cut(bracketed, "]")
		return name, strings.TrimPrefix(rest, ":")
	}
	name, port, _ = strings.Cut(host, ":")
	return name, port
}

// hasEmptyHost reports whether list, the comma-separated value of a host setting, holds an empty host.
func hasEmptyHost(list string) bool {
	return slices.Contains(strings.Split(list, ","), "")
}

// checkAt returns the error for an @ that pgx 5.10 and 5.11 read apart before the first / of rest.
func checkAt(rest string) error {
	before, _, _ := strings.Cut(rest, "/")
	query := strings.IndexAny(before, "?#")
	switch {
	case query >= 0 && strings.LastIndexByte(before, '@') > query:
		return errQueryAt
	case strings.Count(before, "@") > 1:
		return errBareAt
	}
	return nil
}

// checkQuery returns the error for the first shape pgx 5.10 and 5.11 read apart in query, the text after the first ?.
func checkQuery(query string) error {
	switch {
	case strings.ContainsRune(query, '+'):
		return errQueryPlus
	case strings.ContainsRune(query, ';'):
		return errQuerySemicolon
	}
	return checkPairs(query)
}

// checkPairs returns the error for the first key=value pair of query that pgx 5.10 and 5.11 read apart.
func checkPairs(query string) error {
	if query == "" {
		return nil
	}
	seen := make(map[string]bool)
	for pair := range strings.SplitSeq(strings.TrimSuffix(query, "&"), "&") {
		if strings.Count(pair, "=") != 1 {
			return errQueryPair
		}
		key, value := pairOf(pair)
		switch {
		case key == "ssl":
			return errQuerySSL
		case seen[key]:
			return errQueryRepeat
		case key == "host" && hasEmptyHost(value):
			return errEmptyHost
		}
		seen[key] = true
	}
	return nil
}

// pairOf returns the decoded key and value of a query pair, with the key dbname read as database.
func pairOf(pair string) (key, value string) {
	rawKey, rawValue, _ := strings.Cut(pair, "=")
	key, _ = url.PathUnescape(rawKey)
	value, _ = url.PathUnescape(rawValue)
	if key == "dbname" {
		key = "database"
	}
	return key, value
}
