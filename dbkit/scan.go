// SPDX-License-Identifier: Apache-2.0

package dbkit

import "strings"

// byteOrderMark is the UTF-8 byte order mark.
const byteOrderMark = "\xef\xbb\xbf"

// scanned is what one scan learns about a query.
type scanned struct {
	// text is the query as the driver must receive it.
	text string
	// writes reports whether the statement may write.
	writes bool
	// placeholder reports a SQLite question mark, or a $N after a named parameter, outside quotes and comments.
	placeholder bool
}

// scanner is the state of one pass over a query.
type scanner struct {
	// query is the text as the caller wrote it.
	query string
	// sqlite reports whether the engine reads SQLite's quote forms, parameters and spaces.
	sqlite bool
	// pos is the index of the next byte to read.
	pos int
	// out is the driver text built so far, empty until the first swap.
	out strings.Builder
	// copied is the index of the first query byte not yet in out.
	copied int
	// writes reports whether a token read so far makes the statement write.
	writes bool
	// sawWord reports whether a word was read.
	sawWord bool
	// afterSemicolon reports whether a semicolon was read.
	afterSemicolon bool
	// placeholder reports whether a question mark, or a $N after a named parameter, was read on SQLite.
	placeholder bool
	// named reports whether a named parameter was read on SQLite.
	named bool
	// prev is the previous token when it was a word, and empty otherwise.
	prev string
	// prev2 is the token before prev when it and prev were words, and empty otherwise.
	prev2 string
	// prev3 is the token before prev2 when it, prev2 and prev were words, and empty otherwise.
	prev3 string
}

// scan reads query once for engine and returns what it learns.
func scan(query string, engine Engine) scanned {
	s := scanner{query: query, sqlite: engine == SQLite}
	for s.pos < len(s.query) {
		s.step()
	}
	return scanned{text: s.text(), writes: s.writes || !s.sawWord, placeholder: s.placeholder}
}

// text returns the driver text, the query itself when nothing was swapped.
func (s *scanner) text() string {
	if s.out.Cap() == 0 {
		return s.query
	}
	s.out.WriteString(s.query[s.copied:])
	return s.out.String()
}

// swap puts a question mark in place of the dollar sign at index at.
func (s *scanner) swap(at int) {
	if s.out.Cap() == 0 {
		s.out.Grow(len(s.query))
	}
	s.out.WriteString(s.query[s.copied:at])
	s.out.WriteByte('?')
	s.copied = at + 1
}

// step reads one token.
func (s *scanner) step() {
	c := s.query[s.pos]
	switch {
	case isSpaceByte(c):
		s.pos++
	case s.sqlite && strings.HasPrefix(s.query[s.pos:], byteOrderMark):
		s.pos += len(byteOrderMark)
	case isWordStart(c):
		s.word()
	case isDigitByte(c):
		s.number()
	default:
		s.symbol(c)
	}
}

// number reads a number, with the identifier bytes after it on SQLite and its digits alone on PostgreSQL.
func (s *scanner) number() {
	if s.sqlite {
		s.pos = s.identEnd(s.pos + 1)
	} else {
		s.pos = s.digitsEnd(s.pos + 1)
	}
	s.other()
}

// symbol reads a token that starts with a byte outside spaces, words and numbers.
func (s *scanner) symbol(c byte) {
	switch c {
	case '\'', '"':
		s.quoted(c)
	case '`', '[':
		s.sqliteQuote(c)
	case ':', '@', '#':
		s.sqliteVariable()
	case '-':
		s.dash()
	case '/':
		s.slash()
	case '$':
		s.dollar()
	case '?':
		s.question()
	case ';':
		s.semicolon()
	default:
		s.plain()
	}
}

// plain reads one byte that is a token of its own.
func (s *scanner) plain() {
	s.pos++
	s.other()
}

// semicolon reads a semicolon.
func (s *scanner) semicolon() {
	s.plain()
	s.afterSemicolon = true
}

// other records a token that is neither a space, a comment nor a word.
func (s *scanner) other() {
	if s.afterSemicolon {
		s.writes = true
	}
	s.prev3, s.prev2, s.prev = "", "", ""
}

// word reads a word and records whether it makes the statement write.
func (s *scanner) word() {
	start := s.pos
	s.pos = s.identEnd(start + 1)
	w := s.query[start:s.pos]
	if s.afterSemicolon || s.escapeString(w) || s.keywordWrites(w) {
		s.writes = true
	}
	s.sawWord = true
	s.prev3, s.prev2, s.prev = s.prev2, s.prev, w
}

// keywordWrites reports whether w makes the statement write in its place among the words read so far.
func (s *scanner) keywordWrites(w string) bool {
	if !s.sawWord {
		return !opensRead(w)
	}
	if isKeyword(w, "UPDATE") {
		forNoKey := isKeyword(s.prev, "KEY") && isKeyword(s.prev2, "NO") && isKeyword(s.prev3, "FOR")
		return !isKeyword(s.prev, "FOR") && !forNoKey
	}
	return isKeyword(w, "INSERT") || isKeyword(w, "DELETE") || isKeyword(w, "INTO")
}

// escapeString reports whether w opens a PostgreSQL escape string.
func (s *scanner) escapeString(w string) bool {
	return !s.sqlite && (w == "E" || w == "e") && s.byteIs(s.pos, '\'')
}

// quoted reads a string or quoted identifier closed by delim, where a doubled delim stays inside.
func (s *scanner) quoted(delim byte) {
	i := s.pos + 1
	for {
		end := strings.IndexByte(s.query[i:], delim)
		if end < 0 {
			s.unclosed()
			return
		}
		i += end + 1
		if i >= len(s.query) || s.query[i] != delim {
			s.pos = i
			s.other()
			return
		}
		i++
	}
}

// sqliteQuote reads a backtick or bracket identifier on SQLite and a plain byte elsewhere.
func (s *scanner) sqliteQuote(c byte) {
	switch {
	case !s.sqlite:
		s.plain()
	case c == '[':
		s.bracket()
	default:
		s.quoted(c)
	}
}

// bracket reads a bracket identifier up to the first closing bracket.
func (s *scanner) bracket() {
	end := strings.IndexByte(s.query[s.pos+1:], ']')
	if end < 0 {
		s.unclosed()
		return
	}
	s.pos += end + 2
	s.other()
}

// dash reads a line comment or a minus sign.
func (s *scanner) dash() {
	if !s.byteIs(s.pos+1, '-') {
		s.plain()
		return
	}
	end := s.lineEnd(s.pos + 2)
	if end < 0 {
		s.pos = len(s.query)
		return
	}
	s.pos += end + 3
}

// lineEnd returns the offset from i of the byte that ends a line comment, or -1 when the comment runs to the end.
func (s *scanner) lineEnd(i int) int {
	if s.sqlite {
		return strings.IndexByte(s.query[i:], '\n')
	}
	return strings.IndexAny(s.query[i:], "\n\r")
}

// slash reads a block comment up to the first closing mark, or a slash.
func (s *scanner) slash() {
	if !s.byteIs(s.pos+1, '*') {
		s.plain()
		return
	}
	end := strings.Index(s.query[s.pos+2:], "*/")
	if end < 0 {
		s.unclosed()
		return
	}
	if !s.sqlite && strings.Contains(s.query[s.pos+2:s.pos+3+end], "/*") {
		s.writes = true
	}
	s.pos += end + 4
}

// dollar reads a token that starts with a dollar sign.
func (s *scanner) dollar() {
	digits := s.digitsEnd(s.pos + 1)
	switch {
	case !s.sqlite:
		s.postgresDollar(digits)
	case digits > s.pos+1 && !s.identAt(digits):
		s.placeholder = s.placeholder || s.named
		s.swap(s.pos)
		s.pos = digits
		s.other()
	default:
		s.variable()
	}
}

// postgresDollar reads a numbered parameter, a lone dollar sign, or the opening of a dollar quote that writes.
func (s *scanner) postgresDollar(digits int) {
	if digits == s.pos+1 && s.identAt(digits) {
		s.writes = true
	}
	s.pos = digits
	s.other()
}

// sqliteVariable reads a named parameter on SQLite and a plain byte elsewhere.
func (s *scanner) sqliteVariable() {
	if !s.sqlite {
		s.plain()
		return
	}
	s.variable()
}

// variable reads a SQLite named parameter with its double colon and parenthesis forms.
func (s *scanner) variable() {
	end, named := s.variableName(s.pos + 1)
	s.named = true
	s.pos = end
	if named && s.byteIs(end, '(') {
		s.variableCall()
		return
	}
	s.other()
}

// variableName returns the index after the SQLite parameter name that starts at i, and whether it holds a name byte.
func (s *scanner) variableName(i int) (int, bool) {
	named := false
	for i < len(s.query) {
		switch {
		case isIdentByte(s.query[i]):
			named = true
			i++
		case s.query[i] == ':' && s.byteIs(i+1, ':'):
			i += 2
		default:
			return i, named
		}
	}
	return i, named
}

// variableCall reads the parenthesis that ends a SQLite parameter name, up to the first closing parenthesis.
func (s *scanner) variableCall() {
	end := s.pos + 1
	for end < len(s.query) && s.query[end] != ')' && !isSpaceByte(s.query[end]) {
		end++
	}
	if !s.byteIs(end, ')') {
		s.unclosed()
		return
	}
	s.pos = end + 1
	s.other()
}

// question reads a question mark, with the digits SQLite reads after it, and flags it as a placeholder on SQLite.
func (s *scanner) question() {
	s.pos++
	if s.sqlite {
		s.placeholder = true
		s.pos = s.digitsEnd(s.pos)
	}
	s.other()
}

// unclosed ends the scan inside a quote, comment or parameter that never closes.
func (s *scanner) unclosed() {
	s.writes = true
	s.pos = len(s.query)
}

// byteIs reports whether the byte at i exists and equals b.
func (s *scanner) byteIs(i int, b byte) bool {
	return i < len(s.query) && s.query[i] == b
}

// identAt reports whether the byte at i exists and may continue an identifier.
func (s *scanner) identAt(i int) bool {
	return i < len(s.query) && isIdentByte(s.query[i])
}

// identEnd returns the index after the run of identifier bytes that starts at i.
func (s *scanner) identEnd(i int) int {
	for s.identAt(i) {
		i++
	}
	return i
}

// digitsEnd returns the index after the run of ASCII digits that starts at i.
func (s *scanner) digitsEnd(i int) int {
	for i < len(s.query) && isDigitByte(s.query[i]) {
		i++
	}
	return i
}

// opensRead reports whether w may open a statement that only reads.
func opensRead(w string) bool {
	return isKeyword(w, "SELECT") || isKeyword(w, "VALUES") || isKeyword(w, "WITH")
}

// isKeyword reports whether w is the upper case ASCII keyword kw in any letter case.
func isKeyword(w, kw string) bool {
	if len(w) != len(kw) {
		return false
	}
	for i := range len(w) {
		if w[i]&^0x20 != kw[i] {
			return false
		}
	}
	return true
}

// isSpaceByte reports whether c is an ASCII space, tab, line feed, vertical tab, form feed or carriage return.
func isSpaceByte(c byte) bool {
	return c == ' ' || ('\t' <= c && c <= '\r')
}

// isWordStart reports whether c may start a word.
func isWordStart(c byte) bool {
	lower := c | 0x20
	return c >= 0x80 || c == '_' || ('a' <= lower && lower <= 'z')
}

// isDigitByte reports whether c is an ASCII digit.
func isDigitByte(c byte) bool {
	return '0' <= c && c <= '9'
}

// isIdentByte reports whether c may continue an identifier.
func isIdentByte(c byte) bool {
	return isWordStart(c) || isDigitByte(c) || c == '$'
}
