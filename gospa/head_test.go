// SPDX-License-Identifier: Apache-2.0

package gospa

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// head is the meta element the page shapes are written with.
const head = `<meta name="api-root" content="/api/">`

// mark is the byte order mark a page may open with.
const mark = byteOrderMark

// pageShapes are pages of every shape, each with the page withHead makes of it.
var pageShapes = []struct {
	name, page, want string
}{
	{
		name: "a doctype, an html tag and a head tag",
		page: `<!doctype html><html><head><title>App</title></head></html>`,
		want: `<!doctype html><html><head>` + head + `<title>App</title></head></html>`,
	},
	{
		name: "space and a comment between the opening tags",
		page: "<!doctype html>\n<!-- built -->\n<html>\n  <head>\n    <title>App</title>",
		want: "<!doctype html>\n<!-- built -->\n<html>\n  <head>" + head + "\n    <title>App</title>",
	},
	{
		name: "an upper case doctype and head",
		page: `<!DOCTYPE HTML><HEAD><TITLE>App</TITLE>`,
		want: `<!DOCTYPE HTML><HEAD>` + head + `<TITLE>App</TITLE>`,
	},
	{
		name: "no doctype",
		page: `<html><head></head>`,
		want: `<html><head>` + head + `</head>`,
	},
	{
		name: "no opening tag before the content",
		page: `<title>App</title>`,
		want: head + `<title>App</title>`,
	},
	{
		name: "an empty page",
		page: ``,
		want: head,
	},
	{
		name: "a head written inside a title",
		page: `<!doctype html><title><head></title><head></head>`,
		want: `<!doctype html>` + head + `<title><head></title><head></head>`,
	},
	{
		name: "a head written inside a script",
		page: `<!doctype html><script>let tag = "<head>"</script><head></head>`,
		want: `<!doctype html>` + head + `<script>let tag = "<head>"</script><head></head>`,
	},
	{
		name: "a head written inside a template",
		page: `<!doctype html><template><head/></template><title>App</title>`,
		want: `<!doctype html>` + head + `<template><head/></template><title>App</title>`,
	},
	{
		name: "an html tag with attributes and no head",
		page: `<!doctype html><html lang="en"><title>App</title>`,
		want: `<!doctype html><html lang="en">` + head + `<title>App</title>`,
	},
	{
		name: "a head written inside an attribute",
		page: `<!doctype html><html data-note="<head>"><head></head>`,
		want: `<!doctype html><html data-note="<head>"><head>` + head + `</head>`,
	},
	{
		name: "a self-closing head",
		page: `<!doctype html><head/><title>App</title>`,
		want: `<!doctype html><head/>` + head + `<title>App</title>`,
	},
	{
		name: "an empty comment closed at once",
		page: `<!doctype html><!--><head></head>`,
		want: `<!doctype html><!--><head>` + head + `</head>`,
	},
	{
		name: "a processing instruction before the doctype",
		page: `<?xml version="1.0"?><!doctype html><title>App</title>`,
		want: `<?xml version="1.0"?><!doctype html>` + head + `<title>App</title>`,
	},
	{
		name: "a doctype that never closes",
		page: `<!doctype html`,
		want: head + `<!doctype html`,
	},
	{
		name: "a byte order mark before the doctype",
		page: mark + `<!doctype html><title>App</title>`,
		want: mark + `<!doctype html>` + head + `<title>App</title>`,
	},
	{
		name: "a byte order mark alone",
		page: mark,
		want: mark + head,
	},
	{
		name: "a no-break space before the head",
		page: "<!doctype html> <head><title>App</title>",
		want: "<!doctype html>" + head + " <head><title>App</title>",
	},
	{
		name: "a vertical tab before the head",
		page: "<!doctype html>\v<head><title>App</title>",
		want: "<!doctype html>" + head + "\v<head><title>App</title>",
	},
	{
		name: "every HTML white space before the head",
		page: "<!doctype html> \t\n\r\f<head><title>App</title>",
		want: "<!doctype html> \t\n\r\f<head>" + head + "<title>App</title>",
	},
	{
		name: "a body as the first tag",
		page: `<!doctype html><html><body><div id="root"></div></body></html>`,
		want: `<!doctype html><html>` + head + `<body><div id="root"></div></body></html>`,
	},
	{
		name: "a byte order mark after the doctype",
		page: `<!doctype html>` + mark + `<head><title>App</title>`,
		want: `<!doctype html>` + head + mark + `<head><title>App</title>`,
	},
}

func TestWithHeadWritesTheValuesWhereThePageOpens(t *testing.T) {
	t.Parallel()

	for _, tc := range pageShapes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := string(withHead([]byte(tc.page), []byte(head)))
			if got != tc.want {
				t.Errorf("withHead(%q) = %q, want %q", tc.page, got, tc.want)
			}
		})
	}
}

func TestMetaElementsWritesEachValueInOrderEscaped(t *testing.T) {
	t.Parallel()

	got, err := metaElements([]Meta{
		{Name: "api-root", Content: "/api/"},
		{Name: `say "hi"`, Content: `it's <b>&</b>`},
		{Name: "empty", Content: ""},
	})
	want := `<meta name="api-root" content="/api/">` +
		`<meta name="say &#34;hi&#34;" content="it&#39;s &lt;b&gt;&amp;&lt;/b&gt;">` +
		`<meta name="empty" content="">`
	if err != nil || string(got) != want {
		t.Errorf("metaElements() = %q, %v, want %q", got, err, want)
	}
}

func TestMetaElementsWritesNothingWithoutValues(t *testing.T) {
	t.Parallel()

	got, err := metaElements(nil)
	if err != nil || got != nil {
		t.Errorf("metaElements(nil) = %q, %v, want nil", got, err)
	}
}

func TestMetaElementsRejectsAValueItCannotName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		values []Meta
		want   string
	}{
		{
			name:   "a value with no name",
			values: []Meta{{Name: "api-root", Content: "/api/"}, {Content: "30"}},
			want:   "gospa: page value 1 has no name",
		},
		{
			name:   "a name given twice",
			values: []Meta{{Name: "api-root", Content: "/api/"}, {Name: "api-root", Content: "/v2/"}},
			want:   `gospa: page value "api-root" is given twice`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := metaElements(tc.values)
			if err == nil {
				t.Fatalf("metaElements() = %q, want the error %q", got, tc.want)
			}
			if err.Error() != tc.want || got != nil {
				t.Errorf("metaElements() = %q, %q, want nothing and the error %q", got, err, tc.want)
			}
		})
	}
}

func FuzzWithHeadPutsTheValuesInTheHead(f *testing.F) {
	for _, page := range []string{
		``,
		`<!doctype html><html lang="en"><head><title>App</title></head><body></body></html>`,
		"<!doctype html>\n<!-- built -->\n<html>\n  <head>\n    <title>App</title>",
		`<!doctype html><template><head/></template><title>App</title>`,
		`<?xml version="1.0"?><!doctype html><title>App</title>`,
		`<!doctype html`,
		mark + `<!doctype html><title>App</title>`,
		`<!doctype html>` + mark + `<head><title>App</title>`,
		`<html><frameset></frameset></html>`,
		"\x00<head>",
		"<!doctype html> <head><title>App</title>",
		"<!doctype html>\v<head><title>App</title>",
		`<!doctype html><html><body><div id="root"></div></body></html>`,
	} {
		f.Add(page)
	}
	values := `<meta name="gospa-fuzz" content="1"><meta name="gospa-fuzz" content="2">`
	f.Fuzz(func(t *testing.T, page string) {
		if len(fuzzValues(t, page)) > 0 {
			t.Skip("the page already holds the values")
		}
		out := string(withHead([]byte(page), []byte(values)))
		if strings.Replace(out, values, "", 1) != page {
			t.Fatalf("withHead(%q) = %q, which is not the page with the values added", page, out)
		}
		if got := fuzzValues(t, out); !slices.Equal(got, []string{"1", "2"}) {
			t.Errorf("withHead(%q) = %q, whose head holds the values %q, want [1 2]", page, out, got)
		}
	})
}

// fuzzValues returns, in order, the content of each gospa-fuzz meta element in page as a browser decodes it.
func fuzzValues(t *testing.T, page string) []string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(strings.TrimPrefix(page, byteOrderMark)))
	if err != nil {
		t.Fatalf("parse %q: %v", page, err)
	}
	var values []string
	for node := range doc.Descendants() {
		if node.DataAtom != atom.Meta || attribute(node, "name") != "gospa-fuzz" {
			continue
		}
		if node.Parent == nil || node.Parent.DataAtom != atom.Head {
			values = append(values, "outside the head")
			continue
		}
		values = append(values, attribute(node, "content"))
	}
	return values
}

// attribute returns the value of the attribute key on node, or an empty string when node has none.
func attribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}
