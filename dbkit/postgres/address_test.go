// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/postgres"
)

// addressBareAt is the error every address with a bare @ before its host answers.
const addressBareAt = "dbkit: refused database address: the user or password holds a bare @, write each one as %40"

// addressSecret is the part of every password the address tests write.
const addressSecret = "secretword"

// addressEmpty is the error an empty or blank address answers.
const addressEmpty = "dbkit: refused database address: the address is empty or blank, " +
	"and pgx would read every setting from the PG* environment variables"

// addressBlanks are addresses that hold nothing but whitespace, the empty one first.
var addressBlanks = []string{"", " ", "\t\n\r\v\f"}

func TestCheckAddressRefusesAnEmptyAddress(t *testing.T) {
	t.Parallel()

	for _, address := range addressBlanks {
		err := postgres.CheckAddress(address)

		if !errors.Is(err, dbkit.ErrAddress) || err.Error() != addressEmpty {
			t.Errorf("CheckAddress(a blank address of %d bytes) error = %v, want %q marked ErrAddress", len(address), err,
				addressEmpty)
		}
	}
}

func TestAddressWithABareAtIsRefused(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"postgres://u:" + addressSecret + "@ss@db/x",
		"postgresql://u:" + addressSecret + "@ss@db/x",
		"postgres://u:" + addressSecret + "@ss@db",
		"postgres://u:" + addressSecret + "@ss@db?sslmode=disable",
		"postgres://u:" + addressSecret + "@ss@db#part",
		"postgres://u@" + addressSecret + ":p@db/x",
		"postgres://u:" + addressSecret + "@@db/x",
		"postgres://u:" + addressSecret + "@ss@db:5432,other:5433/x",
	} {
		err := postgres.CheckAddress(address)

		if !errors.Is(err, dbkit.ErrAddress) || err.Error() != addressBareAt {
			t.Errorf("CheckAddress() error = %v, want %q marked ErrAddress", err, addressBareAt)
		}
		if err != nil && strings.Contains(err.Error(), addressSecret) {
			t.Errorf("CheckAddress() error = %v, want no part of the password", err)
		}
	}
}

// addressQueryAt is the error every address with a ? or # before an @ ahead of its first / answers.
const addressQueryAt = "dbkit: refused database address: a ? or # comes before an @ ahead of the first /, " +
	"write ? as %3F, # as %23 and @ as %40"

func TestAddressWithAQuestionMarkOrHashBeforeItsAtIsRefused(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"postgres://site:pa#" + addressSecret + "@db/site",
		"postgres://site:pa?" + addressSecret + "@db/site",
		"postgres://db?sslpassword=" + addressSecret + "@b",
		"postgresql://db?sslpassword=" + addressSecret + "@b",
		"postgres://u:p@db?sslpassword=" + addressSecret + "@b",
		"postgres://u:p@db?options=" + addressSecret + "@b",
		"postgres://db#" + addressSecret + "@b",
		"postgres://u:p@db#" + addressSecret + "@b",
		"postgres://db?sslmode=disable#" + addressSecret + "@b",
	} {
		err := postgres.CheckAddress(address)

		if !errors.Is(err, dbkit.ErrAddress) || err.Error() != addressQueryAt {
			t.Errorf("CheckAddress() error = %v, want %q marked ErrAddress", err, addressQueryAt)
		}
		if err != nil && strings.Contains(err.Error(), addressSecret) {
			t.Errorf("CheckAddress() error = %v, want no part of the address", err)
		}
	}
}

func TestCheckURLAcceptsKeywordValue(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"host=db user=u password=p@ss",
		"host=db user=u password='p@ss@word'",
		"user=u@x password=p@ss@word host=db",
		"host=db ssl=true application_name=x+y#z sslmode=disable sslmode=require",
		"host=a,b port=5433 dbname='my db' password=",
		" host = 'a' ",
	} {
		if err := postgres.CheckAddress(address); err != nil {
			t.Errorf("CheckAddress(a keyword and value address) error = %v, want nil", err)
		}
	}
}

func TestCheckAddressAcceptsAnEncodedAt(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"postgres://u:p%40ss@db/x",
		"postgresql://u:p%40ss%40word@db/x",
		"postgres://u%40x:p%40ss@db/x",
		"postgres://u:p@db/x",
		"postgres://db/x",
		"postgres://u:p@db/x@y",
		"postgres://u:p@db/x?o=a@b",
		"postgres://u:p%40@db",
		"postgres://u:p@db?sslmode=disable",
		"postgres://db/?sslpassword=a@b",
		"postgres://db?sslpassword=a%40b",
		"postgres://site:pa%23ss@db/site%23primary?application_name=a%23b",
		"postgres://u+v:p+ss@d+b/x+y?application_name=a%2Bb%20c",
		"postgres://u:p%2fss%25@db/caf%C3%A9?application_name=%25",
		"postgres://u:p@db/x?dbname=y&user=v&host=h&port=5433&sslmode=disable&sslmodes=x",
		"postgres://u:p;ss@db/x;y?application_name=a%3Bb",
		"postgres://u:p@db/x?options=-c%20search_path%3Dfoo&=v&sslmode=disable&",
		"postgres://u:p@db/x?",
		"postgres://u:p@h1:5433,h2:5434/x",
		"postgres://u:p@h1,h2/x?host=h3,h4&port=5433,5434",
		"postgres://u:p@db:/x",
		"postgres://:5433/x",
		"postgres:///x?host=%2Ftmp",
		"postgres://u:p@db/x%2Fy/z?application_name=a/b",
		"postgres://db/?dbname=%2Fx",
		"postgres://db?dbname=x/y",
		"postgres://u:p@db/café?application_name=café%20au%20lait",
		"postgres://U-s.e_r~0:P!$&'()*+,;=:-._~9@db/x?application_name=[a]{b}|c^d",
		"postgres://",
	} {
		if err := postgres.CheckAddress(address); err != nil {
			t.Errorf("CheckAddress(an address pgx 5.10 and 5.11 read alike) error = %v, want nil", err)
		}
	}
}

// refusedAddress is one address CheckAddress refuses and the error it answers.
type refusedAddress struct {
	// address is the address under test, holding addressSecret.
	address string
	// want is the error the address answers.
	want string
}

// checkAddressRefuses fails t unless CheckAddress answers each address with its error, marked ErrAddress.
func checkAddressRefuses(t *testing.T, addresses []refusedAddress) {
	t.Helper()
	for _, r := range addresses {
		err := postgres.CheckAddress(r.address)

		if !errors.Is(err, dbkit.ErrAddress) || err.Error() != r.want {
			t.Errorf("CheckAddress() error = %v, want %q marked ErrAddress", err, r.want)
		}
		if err != nil && strings.Contains(err.Error(), addressSecret) {
			t.Errorf("CheckAddress() error = %v, want no part of the address", err)
		}
	}
}

// addressHash is the error every URL address with a raw # answers.
const addressHash = "dbkit: refused database address: the address holds a #, write it as %23"

func TestAddressWithAHashIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://db#a%40" + addressSecret, addressHash},
		{"postgres://good.example#" + addressSecret + "/?host=other.example", addressHash},
		{"postgres://u:p@good.example/site#" + addressSecret + "?host=other.example", addressHash},
		{"postgres://u:p@db/x?application_name=a#" + addressSecret + "&sslmode=verify-full", addressHash},
		{"postgres://u:p@db/x?sslmode=disable#" + addressSecret, addressHash},
		{"postgresql://u:" + addressSecret + "@db/site#primary", addressHash},
	})
}

// addressQueryPlus is the error every URL address with a raw + in its query answers.
const addressQueryPlus = "dbkit: refused database address: the query holds a +, write it as %2B, or a space as %20"

func TestAddressWithAPlusInItsQueryIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:p@db/x?application_name=a+" + addressSecret, addressQueryPlus},
		{"postgres://site:pw@db/site?sslmode=disable&application_name=" + addressSecret + "+b", addressQueryPlus},
		{"postgres://db?options=" + addressSecret + "+x", addressQueryPlus},
		{"postgres://db/x?a" + addressSecret + "+b=c", addressQueryPlus},
	})
}

// addressEscape is the error every URL address with a % that starts no escape or escapes a NUL answers.
const addressEscape = "dbkit: refused database address: a % starts no escape or escapes a NUL byte, write a % as %25"

func TestAddressWithABadEscapeIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:p@db/x?sslmode=verify-full&" + addressSecret + "=%zz", addressEscape},
		{"postgres://u:p@db/x?" + addressSecret + "=1&sslmode=verify-full%", addressEscape},
		{"postgres://u:p@db/x?" + addressSecret + "=%4", addressEscape},
		{"postgres://u:p@db/x?application_name=%00" + addressSecret, addressEscape},
		{"postgres://u:p%00" + addressSecret + "@db/x", addressEscape},
		{"postgres://u:" + addressSecret + "%zz@db/x", addressEscape},
		{"postgres://u:p@db/" + addressSecret + "%g0", addressEscape},
	})
}

// addressQueryRepeat is the error every URL address that sets one query key twice answers.
const addressQueryRepeat = "dbkit: refused database address: the query sets a key twice, " +
	"set each key once, with dbname and database as one key"

func TestAddressThatSetsAQueryKeyTwiceIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:p@db/x?sslmode=require&application_name=" + addressSecret + "&sslmode=disable",
			addressQueryRepeat},
		{"postgres://u:p@db/x?host=" + addressSecret + "&host=second", addressQueryRepeat},
		{"postgres://u:p@db/x?dbname=" + addressSecret + "&dbname=second", addressQueryRepeat},
		{"postgres://u:p@db/x?dbname=" + addressSecret + "&database=second", addressQueryRepeat},
		{"postgres://u:p@db/x?sslm%6Fde=disable&sslmode=" + addressSecret, addressQueryRepeat},
		{"postgres://u:p@db/x?password=" + addressSecret + "&password=" + addressSecret, addressQueryRepeat},
	})
}

// addressQuerySemicolon is the error every URL address with a raw semicolon in its query answers.
const addressQuerySemicolon = "dbkit: refused database address: the query holds a semicolon, write it as %3B"

func TestAddressWithASemicolonInItsQueryIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:p@db/x?application_name=a;" + addressSecret, addressQuerySemicolon},
		{"postgres://u:p@db/x?sslmode=verify-full;" + addressSecret, addressQuerySemicolon},
		{"postgres://u:p@db?sslmode=verify-full&application_name=" + addressSecret + ";b", addressQuerySemicolon},
	})
}

// addressQueryPair is the error every URL address with a query pair of no = or two answers.
const addressQueryPair = "dbkit: refused database address: a query pair lacks its = or holds a second one, " +
	"write a = inside a value as %3D"

func TestAddressWithAQueryPairOfNoOrTwoEqualsIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:p@db/x?" + addressSecret, addressQueryPair},
		{"postgres://u:p@db/x?options=-c" + addressSecret + "=foo", addressQueryPair},
		{"postgres://u:p@db/x?&" + addressSecret + "=x", addressQueryPair},
		{"postgres://u:p@db/x?a=b&&" + addressSecret + "=c", addressQueryPair},
		{"postgres://u:p@db/x?&", addressQueryPair},
	})
}

// addressQuerySSL is the error every URL address whose query sets ssl answers.
const addressQuerySSL = "dbkit: refused database address: the query sets ssl, set sslmode instead"

func TestAddressWhoseQuerySetsSSLIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:p@db/x?ssl=true&application_name=" + addressSecret, addressQuerySSL},
		{"postgres://u:p@db/x?application_name=" + addressSecret + "&ssl=false", addressQuerySSL},
		{"postgres://u:p@db?ss%6C=true&sslmode=disable&application_name=" + addressSecret, addressQuerySSL},
	})
}

// addressMixedPorts is the error every URL address whose host list gives a port to some hosts only answers.
const addressMixedPorts = "dbkit: refused database address: some hosts of the host list carry a port " +
	"and others do not, give every host a port or none"

func TestAddressWhoseHostListGivesSomeHostsAPortIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:" + addressSecret + "@h1,h2:5433/x", addressMixedPorts},
		{"postgres://u:" + addressSecret + "@h1:5433,h2/x", addressMixedPorts},
		{"postgres://u:" + addressSecret + "@h1,h2,h3:5433/x?sslmode=disable", addressMixedPorts},
		{"postgres://u:" + addressSecret + "@h1:,h2:5433/x", addressMixedPorts},
		{"postgres://u:" + addressSecret + "@[::1],h2:5433/x", addressMixedPorts},
		{"postgres://h1:5433,h2?application_name=" + addressSecret, addressMixedPorts},
	})
}

// addressEmptyHost is the error every address whose host list holds an empty host answers.
const addressEmptyHost = "dbkit: refused database address: the host list holds an empty host, name every host"

func TestAddressWhoseHostListHoldsAnEmptyHostIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:" + addressSecret + "@,h2/x", addressEmptyHost},
		{"postgres://u:" + addressSecret + "@h1,,h2/x", addressEmptyHost},
		{"postgres://u:" + addressSecret + "@h1,/x", addressEmptyHost},
		{"postgres://u:" + addressSecret + "@:5433,h2:5434/x", addressEmptyHost},
		{"postgres://u:p@db/x?host=a,,b&application_name=" + addressSecret, addressEmptyHost},
		{"postgres://u:p@db/x?application_name=" + addressSecret + "&ho%73t=a%2C", addressEmptyHost},
		{"postgres://u:p@db/x?application_name=" + addressSecret + "&host=", addressEmptyHost},
		{"host=a,,b password=" + addressSecret, addressEmptyHost},
		{"host=a, password=" + addressSecret, addressEmptyHost},
		{"password=" + addressSecret + " host='a,,b'", addressEmptyHost},
		{"password=" + addressSecret + "\thost = ''", addressEmptyHost},
		{"host=,b dbname=x password='" + addressSecret + "'", addressEmptyHost},
	})
}

// addressSlashDatabase is the error every URL address whose path gives a database name that starts with a / answers.
const addressSlashDatabase = "dbkit: refused database address: the database name starts with a /, " +
	"give it as ?dbname= with the / written as %2F"

func TestAddressWhoseDatabaseNameStartsWithASlashIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:" + addressSecret + "@db//x", addressSlashDatabase},
		{"postgres://u:" + addressSecret + "@db/%2Fx", addressSlashDatabase},
		{"postgres://u:p@db/%2f" + addressSecret + "?sslmode=disable", addressSlashDatabase},
		{"postgres://db///" + addressSecret, addressSlashDatabase},
	})
}

// addressSpace is the error every URL address with a raw space or control character answers.
const addressSpace = "dbkit: refused database address: the address holds a space or a control character, " +
	"write a space as %20"

func TestAddressWithASpaceOrControlCharacterIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:p@db/x?application_name=a " + addressSecret, addressSpace},
		{"postgres://u:p@db/" + addressSecret + " ", addressSpace},
		{"postgres://u:p@db/ " + addressSecret, addressSpace},
		{"postgres://u:p " + addressSecret + "@db/x", addressSpace},
		{"postgres://u: " + addressSecret + "@db/x", addressSpace},
		{"postgres://u:p@db/" + addressSecret + "\t", addressSpace},
		{"postgres://u:p@db/x?application_name=" + addressSecret + "\n", addressSpace},
		{"postgres://u:p@db/x?application_name=" + addressSecret + "\x7f", addressSpace},
		{"postgres://u:p@db/x?application_name=" + addressSecret + "\x00", addressSpace},
	})
}

// addressUserCharacter is the error of every URL address whose user or password holds a character pgx 5.10 refuses.
const addressUserCharacter = "dbkit: refused database address: the user or password holds a character " +
	"pgx 5.10 cannot read, write it percent-encoded"

func TestAddressWithACharacterPgx510CannotReadInItsUserOrPasswordIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"postgres://u:" + addressSecret + "^x@db/x", addressUserCharacter},
		{"postgres://u:p[" + addressSecret + "]@db/x", addressUserCharacter},
		{"postgres://u:" + addressSecret + "{x}|y@db/x", addressUserCharacter},
		{"postgres://u:" + addressSecret + "`<>\"@db/x", addressUserCharacter},
		{"postgres://u:" + addressSecret + "\\x@db/x", addressUserCharacter},
		{"postgres://u\u00e4:" + addressSecret + "@db/x", addressUserCharacter},
	})
}

// addressBackslash is the error every keyword and value address with a backslash answers.
const addressBackslash = "dbkit: refused database address: the keyword and value address holds a backslash, " +
	"write the address as a URL with %5C"

func TestKeywordValueAddressWithABackslashIsRefused(t *testing.T) {
	t.Parallel()

	checkAddressRefuses(t, []refusedAddress{
		{"host=go\\od.example password=" + addressSecret, addressBackslash},
		{"host=good.example\\,other.example password=" + addressSecret, addressBackslash},
		{"host=db password=" + addressSecret + "\\", addressBackslash},
		{"host=db password='p\\'" + addressSecret + "'", addressBackslash},
		{"host=db\\ x password=" + addressSecret, addressBackslash},
	})
}
