// SPDX-License-Identifier: Apache-2.0

package gospa

import (
	"bytes"
	"fmt"
	"slices"

	"golang.org/x/net/html"
)

// byteOrderMark is the mark a UTF-8 page may open with.
const byteOrderMark = string(rune(0xFEFF))

// Meta is one server value the page carries in its head as a meta element.
type Meta struct {
	// Name is the name the app looks the value up by.
	Name string
	// Content is the value the app reads.
	Content string
}

// metaElements returns the meta elements of values in order, nil when there are none.
func metaElements(values []Meta) ([]byte, error) {
	var elements []byte
	seen := make(map[string]bool, len(values))
	for i, value := range values {
		if value.Name == "" {
			return nil, fmt.Errorf("gospa: page value %d has no name", i)
		}
		if seen[value.Name] {
			return nil, fmt.Errorf("gospa: page value %q is given twice", value.Name)
		}
		seen[value.Name] = true
		elements = fmt.Appendf(elements, `<meta name="%s" content="%s">`,
			html.EscapeString(value.Name), html.EscapeString(value.Content))
	}
	return elements, nil
}

// withHead returns page with head written just after the doctype, html and head tags it opens with.
func withHead(page, head []byte) []byte {
	at := headOffset(page)
	return slices.Concat(page[:at], head, page[at:])
}

// headOffset returns the offset just after the doctype, html and head tags the page opens with.
func headOffset(page []byte) int {
	start := 0
	if bytes.HasPrefix(page, []byte(byteOrderMark)) {
		start = len(byteOrderMark)
	}
	tokens := html.NewTokenizer(bytes.NewReader(page[start:]))
	offset, at := start, start
	for kind := tokens.Next(); kind != html.ErrorToken; kind = tokens.Next() {
		offset += len(tokens.Raw())
		switch {
		case opensThePage(tokens, kind):
			at = offset
		case !blank(tokens, kind):
			return at
		}
	}
	return at
}

// opensThePage reports whether the current token is a closed doctype or an html or head start tag.
func opensThePage(tokens *html.Tokenizer, kind html.TokenType) bool {
	switch kind {
	case html.DoctypeToken:
		return bytes.HasSuffix(tokens.Raw(), []byte(">"))
	case html.StartTagToken, html.SelfClosingTagToken:
		name, _ := tokens.TagName()
		return string(name) == "html" || string(name) == "head"
	}
	return false
}

// blank reports whether the current token is a comment or text holding only HTML white space.
func blank(tokens *html.Tokenizer, kind html.TokenType) bool {
	if kind == html.TextToken {
		return len(bytes.Trim(tokens.Raw(), " \t\n\r\f")) == 0
	}
	return kind == html.CommentToken
}
