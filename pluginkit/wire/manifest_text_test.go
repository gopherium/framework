// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"fmt"
	"strings"
	"testing"
)

// escapes returns each UTF-16 code unit written as a JSON escape of four hex digits.
func escapes(units ...rune) string {
	var b strings.Builder
	for _, unit := range units {
		fmt.Fprintf(&b, `\u%04x`, unit)
	}
	return b.String()
}

// notUTF8 is the text rejecting a manifest whose bytes are not valid UTF-8.
const notUTF8 = "the manifest holds text that is not valid UTF-8"

// unpaired is the text rejecting a manifest that escapes a surrogate with no pair.
const unpaired = "the manifest holds an escaped surrogate with no pair"

// invalidText lists text that encoding/json v1 reads by replacing it, and the text rejecting each.
func invalidText() map[string]wrongShape {
	return map[string]wrongShape{
		"a cut sequence in a value":         {`"description": "a` + "\xe2\x82" + `b"`, notUTF8},
		"a cut four byte sequence":          {`"description": "a` + "\xf0\x9f\x98" + `b"`, notUTF8},
		"a byte that is never UTF-8":        {`"note": "` + "\xff" + `"`, notUTF8},
		"a byte that is not UTF-8 in a key": {`"no` + "\xff" + `te": 1`, notUTF8},
		"a surrogate written as bytes":      {`"description": "` + "\xed\xa0\x80" + `"`, notUTF8},
		"a lone high surrogate":             {`"description": "` + escapes(0xd800) + `"`, unpaired},
		"a lone low surrogate":              {`"description": "` + escapes(0xdc00) + `"`, unpaired},
		"a reversed surrogate pair":         {`"description": "` + escapes(0xdc00, 0xd800) + `"`, unpaired},
		"two high surrogates":               {`"description": "` + escapes(0xd83d, 0xd83d) + `"`, unpaired},
		"a high surrogate before a letter":  {`"description": "` + escapes(0xd83d) + `a"`, unpaired},
		"a lone surrogate in a key":         {`"` + escapes(0xd800) + `": 1`, unpaired},
		"a lone surrogate after a pair": {
			`"pages": ["` + escapes(0xd83d, 0xde00) + `", "` + escapes(0xde00) + `"]`, unpaired,
		},
		"a lone surrogate inside a setting": {
			`"settings": [{"key": "a", "type": "b", "label": "` + escapes(0xd800) + `"}]`, unpaired,
		},
	}
}

func TestLoadReadsEscapedText(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"description": "caf`+escapes(0xe9)+` `+escapes(0xd83d, 0xde00)+` \\`+`ud800 \" \/ \n x"`)

	assertSame(t, got.Description, "café 😀 "+`\`+"ud800 \" / \n x")
}
