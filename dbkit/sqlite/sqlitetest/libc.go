// SPDX-License-Identifier: Apache-2.0

package sqlitetest

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/gopherium/framework/dbkit/sqlite"
)

// libcPath is the module path of the C library the driver runs on.
const libcPath = "modernc.org/libc"

// CheckLibc fails t when the build's modernc.org/libc differs from sqlite.LibcVersion.
func CheckLibc(t testing.TB) {
	t.Helper()
	info, ok := debug.ReadBuildInfo()
	checkLibc(t, info, ok)
}

// checkLibc fails t unless info, read when ok, links modernc.org/libc at sqlite.LibcVersion.
func checkLibc(t testing.TB, info *debug.BuildInfo, ok bool) {
	t.Helper()
	if !ok {
		t.Errorf("dbkit: the build carries no module information, want %s %s", libcPath, sqlite.LibcVersion)
		return
	}
	linked := libcOf(info.Deps)
	if linked == nil {
		t.Errorf("dbkit: the build holds no %s, want %s", libcPath, sqlite.LibcVersion)
		return
	}
	if linked.Replace != nil {
		linked = linked.Replace
	}
	if linked.Path != libcPath || linked.Version != sqlite.LibcVersion {
		t.Errorf("dbkit: the build links %s in place of %s %s",
			strings.TrimSpace(linked.Path+" "+linked.Version), libcPath, sqlite.LibcVersion)
	}
}

// libcOf returns the modernc.org/libc module among deps, or nil.
func libcOf(deps []*debug.Module) *debug.Module {
	for _, dep := range deps {
		if dep.Path == libcPath {
			return dep
		}
	}
	return nil
}
