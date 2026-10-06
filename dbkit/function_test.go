// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/gopherium/framework/dbkit"
)

// basicEcho returns its first argument.
func basicEcho(args []driver.Value) (driver.Value, error) {
	return args[0], nil
}

// basicFunction returns a valid function named name.
func basicFunction(name string) dbkit.Function {
	return dbkit.Function{Name: name, Args: 1, Deterministic: true, Call: basicEcho}
}

// basicNames returns the name of every function in fns.
func basicNames(fns []dbkit.Function) []string {
	names := make([]string, 0, len(fns))
	for _, fn := range fns {
		names = append(names, fn.Name)
	}
	return names
}

// basicFold runs the case fold function on one argument.
func basicFold(t *testing.T, arg driver.Value) driver.Value {
	t.Helper()
	got, err := dbkit.CaseFold().Call([]driver.Value{arg})
	if err != nil {
		t.Fatalf("casefold(%v) gave %v, want no error", arg, err)
	}
	return got
}

func TestNewFunctionListKeepsItsFunctions(t *testing.T) {
	t.Parallel()

	anyCount := dbkit.Function{Name: "any_count", Args: -1, Call: basicEcho}
	noArgs := dbkit.Function{Name: "no_args", Args: 0, Call: basicEcho}
	list, err := dbkit.NewFunctionList(basicFunction("first"), anyCount, noArgs)
	if err != nil {
		t.Fatalf("NewFunctionList gave %v, want no error", err)
	}

	got := list.All()

	if want := []string{"first", "any_count", "no_args"}; !reflect.DeepEqual(basicNames(got), want) {
		t.Fatalf("All() names = %v, want %v", basicNames(got), want)
	}
	if got[0].Args != 1 || !got[0].Deterministic || got[1].Args != -1 || got[1].Deterministic || got[2].Args != 0 {
		t.Errorf("All() = %+v, want each function as given", got)
	}
	if result, err := got[0].Call([]driver.Value{int64(7)}); err != nil || result != int64(7) {
		t.Errorf("first Call gave %v, %v, want 7, nil", result, err)
	}
}

func TestNewFunctionListKeepsNamesThatFoldApart(t *testing.T) {
	t.Parallel()

	list, err := dbkit.NewFunctionList(basicFunction("i"), basicFunction("İ"))
	if err != nil {
		t.Fatalf("NewFunctionList gave %v, want no error", err)
	}

	if got, want := basicNames(list.All()), []string{"i", "İ"}; !reflect.DeepEqual(got, want) {
		t.Errorf("All() names = %v, want %v", got, want)
	}
}

func TestAllReturnsACopy(t *testing.T) {
	t.Parallel()

	given := []dbkit.Function{basicFunction("first"), basicFunction("second")}
	list, err := dbkit.NewFunctionList(given...)
	if err != nil {
		t.Fatalf("NewFunctionList gave %v, want no error", err)
	}

	given[0].Name = "changedInput"
	returned := list.All()
	if len(returned) != len(given) {
		t.Fatalf("All() = %v, want %d functions", returned, len(given))
	}
	returned[1].Name = "changedOutput"

	if got, want := basicNames(list.All()), []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Errorf("All() names = %v, want %v", got, want)
	}
}

func TestANilFunctionListHoldsNone(t *testing.T) {
	t.Parallel()

	var list *dbkit.FunctionList

	if got := list.All(); got != nil {
		t.Errorf("nil list All() = %v, want nil", got)
	}
}

func TestAnEmptyFunctionListHoldsNone(t *testing.T) {
	t.Parallel()

	list, err := dbkit.NewFunctionList()
	if err != nil || list == nil {
		t.Fatalf("NewFunctionList() = %v, %v, want a list and no error", list, err)
	}

	if got := list.All(); len(got) != 0 {
		t.Errorf("empty list All() = %v, want no function", got)
	}
}

func TestNewFunctionListRefusesABadFunction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fns  []dbkit.Function
		want string
	}{
		{
			"empty name",
			[]dbkit.Function{basicFunction("first"), {Args: 1, Call: basicEcho}},
			"dbkit: function 1 has an empty name",
		},
		{
			"count below any",
			[]dbkit.Function{{Name: "below_any", Args: -2, Call: basicEcho}},
			`dbkit: function "below_any" takes Args -2, want -1 for any count or 0 and more`,
		},
		{
			"nil call",
			[]dbkit.Function{{Name: "uncalled", Args: 1}},
			`dbkit: function "uncalled" has a nil Call`,
		},
		{
			"same name",
			[]dbkit.Function{basicFunction("twice"), basicFunction("twice")},
			`dbkit: function "twice" repeats the name of function "twice"`,
		},
		{
			"name in another case",
			[]dbkit.Function{basicFunction("fold"), basicFunction("other"), basicFunction("FOLD")},
			`dbkit: function "FOLD" repeats the name of function "fold"`,
		},
		{
			"name in another Unicode case",
			[]dbkit.Function{basicFunction("ñandú"), basicFunction("ÑANDÚ")},
			`dbkit: function "ÑANDÚ" repeats the name of function "ñandú"`,
		},
	}
	for _, c := range cases {
		list, err := dbkit.NewFunctionList(c.fns...)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: NewFunctionList gave %v, want %q", c.name, err, c.want)
		}
		if list != nil {
			t.Errorf("%s: NewFunctionList returned a list beside its error", c.name)
		}
	}
}

func TestCaseFoldDescribesItself(t *testing.T) {
	t.Parallel()

	fn := dbkit.CaseFold()

	if fn.Name != "casefold" || fn.Args != 1 || !fn.Deterministic || fn.Call == nil {
		t.Errorf("CaseFold() = %+v, want casefold with one argument, deterministic and callable", fn)
	}
	if _, err := dbkit.NewFunctionList(fn); err != nil {
		t.Errorf("NewFunctionList(CaseFold()) gave %v, want no error", err)
	}
}

func TestCaseFoldFoldsUnicodeText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text string
		want string
	}{
		{"Ñandú", "ñandú"},
		{"ñandú", "ñandú"},
		{"ΣΑΣ", "σασ"},
		{"σας", "σασ"},
		{"K", "k"},
		{"K", "k"},
		{"ǅ", "ǆ"},
		{"ẞ", "ß"},
		{"İSTANBUL", "İstanbul"},
		{"plain ascii 42", "plain ascii 42"},
		{"", ""},
	}
	for _, c := range cases {
		if got := basicFold(t, c.text); got != c.want {
			t.Errorf("casefold(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestCaseFoldAgreesWithEqualFold(t *testing.T) {
	t.Parallel()

	pairs := [][2]string{
		{"Ñandú", "ñandú"},
		{"ΣΑΣ", "σας"},
		{"Kelvin", "kelvin"},
		{"Hello World", "hELLO wORLD"},
		{"ǅungla", "ǆUNGLA"},
		{"\U00010400", "\U00010428"},
		{"straße", "STRAẞE"},
	}
	for _, pair := range pairs {
		if !strings.EqualFold(pair[0], pair[1]) {
			t.Fatalf("%q and %q do not fold equal under strings.EqualFold", pair[0], pair[1])
		}
		if a, b := basicFold(t, pair[0]), basicFold(t, pair[1]); a != b {
			t.Errorf("casefold(%q) = %q and casefold(%q) = %q, want them equal", pair[0], a, pair[1], b)
		}
	}
	call := dbkit.CaseFold().Call
	args := make([]driver.Value, 1)
	for r := rune(0); r <= unicode.MaxRune; r++ {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if a, b := basicFold(t, string(r)), basicFold(t, string(f)); a != b {
				t.Fatalf("casefold(%q) = %q and casefold(%q) = %q, want them equal", r, a, f, b)
			}
		}
		args[0] = string(r)
		got, err := call(args)
		if folded, _ := got.(string); err != nil || !strings.EqualFold(folded, string(r)) {
			t.Fatalf("casefold(%q) = %#v, %v, want a text that strings.EqualFold matches to %q", r, got, err, r)
		}
	}
}

func TestCaseFoldKeepsApartWhatEqualFoldKeepsApart(t *testing.T) {
	t.Parallel()

	pairs := [][2]string{
		{"İ", "i"},
		{"İ", "I"},
		{"İstanbul", "istanbul"},
	}
	for _, pair := range pairs {
		if strings.EqualFold(pair[0], pair[1]) {
			t.Fatalf("%q and %q fold equal under strings.EqualFold", pair[0], pair[1])
		}
		if a, b := basicFold(t, pair[0]), basicFold(t, pair[1]); a == b {
			t.Errorf("casefold(%q) and casefold(%q) both = %q, want them different", pair[0], pair[1], a)
		}
	}
}

func TestCaseFoldKeepsInvalidBytes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text string
		want string
	}{
		{"\xff", "\xff"},
		{"\xfe", "\xfe"},
		{"A\xffB", "a\xffb"},
		{"\xc3", "\xc3"},
		{"Ñ\xc3\xffÚ", "ñ\xc3\xffú"},
		{"�", "�"},
	}
	for _, c := range cases {
		if got := basicFold(t, c.text); got != c.want {
			t.Errorf("casefold(%q) = %q, want %q", c.text, got, c.want)
		}
	}
	if a, b := basicFold(t, "\xff"), basicFold(t, "\xfe"); a == b {
		t.Errorf("casefold folds two different invalid bytes to the same %q", a)
	}
}

func TestCaseFoldKeepsNull(t *testing.T) {
	t.Parallel()

	if got := basicFold(t, nil); got != nil {
		t.Errorf("casefold(NULL) = %v, want NULL", got)
	}
}

func TestCaseFoldReadsBytesAsText(t *testing.T) {
	t.Parallel()

	got := basicFold(t, []byte("ÑANDÚ"))

	if text, ok := got.(string); !ok || text != "ñandú" {
		t.Errorf("casefold([]byte) = %#v, want the string ñandú", got)
	}
}

func TestCaseFoldLeavesOtherValuesUnchanged(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, value := range []driver.Value{int64(42), float64(1.5), true, at} {
		if got := basicFold(t, value); got != value {
			t.Errorf("casefold(%#v) = %#v, want it unchanged", value, got)
		}
	}
}

func TestCaseFoldRefusesAnotherArgumentCount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []driver.Value
		want string
	}{
		{nil, "dbkit: casefold takes one argument, got 0"},
		{[]driver.Value{"first", "second"}, "dbkit: casefold takes one argument, got 2"},
	}
	call := dbkit.CaseFold().Call
	for _, c := range cases {
		if _, err := call(c.args); err == nil || err.Error() != c.want {
			t.Errorf("casefold with %d arguments gave %v, want %q", len(c.args), err, c.want)
		}
	}
}

func BenchmarkCaseFold(b *testing.B) {
	cases := []struct {
		name string
		text string
	}{
		{"lowercase ascii", "plain ascii text of some length"},
		{"mixed unicode", "Ñandú ΣΑΣ Kelvin STRAẞE"},
	}
	call := dbkit.CaseFold().Call
	for _, c := range cases {
		args := []driver.Value{c.text}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := call(args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
