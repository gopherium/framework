// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bytes"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

func TestEncodeWritesOneIndentedDocument(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	err := gonsole.Call{Stdout: &out}.Encode(map[string]any{"title": "Q3 <draft> & notes", "applied": true})

	if err != nil {
		t.Fatalf("Encode() error = %v, want nil", err)
	}
	want := `{
  "applied": true,
  "title": "Q3 <draft> & notes"
}
`
	if out.String() != want {
		t.Errorf("document = %q, want %q", out.String(), want)
	}
}

func TestEncodeRefusesAValueJSONCannotHold(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	err := gonsole.Call{Stdout: &out}.Encode(make(chan int))

	if err == nil {
		t.Errorf("Encode() error = nil, want an error")
	}
	if out.Len() != 0 {
		t.Errorf("document = %q, want nothing written", out.String())
	}
}
