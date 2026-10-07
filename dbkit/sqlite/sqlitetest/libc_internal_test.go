// SPDX-License-Identifier: Apache-2.0

package sqlitetest

import (
	"runtime/debug"
	"slices"
	"testing"

	"github.com/gopherium/framework/dbkit/sqlite"
)

const (
	// libcModule is the module path of the C library the driver runs on.
	libcModule = "modernc.org/libc"
	// libcOtherVersion is a modernc.org/libc version other than the pinned one.
	libcOtherVersion = "v1.0.0"
)

// libcBuild returns the build information of a binary that links deps.
func libcBuild(deps ...*debug.Module) *debug.BuildInfo {
	return &debug.BuildInfo{Path: "example.com/consumer", Deps: deps}
}

func TestCheckLibc(t *testing.T) {
	t.Parallel()

	pinned := &debug.Module{Path: libcModule, Version: sqlite.LibcVersion}
	driver := &debug.Module{Path: "modernc.org/sqlite", Version: "v1.60.1"}
	cases := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want []string
	}{
		{"the pinned version", libcBuild(driver, pinned), true, nil},
		{"a replacement by the pinned version",
			libcBuild(&debug.Module{Path: libcModule, Version: libcOtherVersion, Replace: pinned}), true, nil},
		{"no build information", nil, false,
			[]string{"dbkit: the build carries no module information, want modernc.org/libc " + sqlite.LibcVersion}},
		{"no libc in the build", libcBuild(driver), true,
			[]string{"dbkit: the build holds no modernc.org/libc, want " + sqlite.LibcVersion}},
		{"another version", libcBuild(driver, &debug.Module{Path: libcModule, Version: libcOtherVersion}), true,
			[]string{"dbkit: the build links modernc.org/libc " + libcOtherVersion + " in place of modernc.org/libc " +
				sqlite.LibcVersion}},
		{"a replacement by a fork", libcBuild(&debug.Module{Path: libcModule, Version: sqlite.LibcVersion,
			Replace: &debug.Module{Path: "example.com/libc-fork", Version: sqlite.LibcVersion}}), true,
			[]string{"dbkit: the build links example.com/libc-fork " + sqlite.LibcVersion +
				" in place of modernc.org/libc " + sqlite.LibcVersion}},
		{"a replacement by a local folder", libcBuild(&debug.Module{Path: libcModule, Version: sqlite.LibcVersion,
			Replace: &debug.Module{Path: "../libc"}}), true,
			[]string{"dbkit: the build links ../libc in place of modernc.org/libc " + sqlite.LibcVersion}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			r := runRecorded(t, func(tb testing.TB) { checkLibc(tb, c.info, c.ok) })

			if !slices.Equal(r.errs, c.want) {
				t.Errorf("Errorf messages = %q, want %q", r.errs, c.want)
			}
		})
	}
}
