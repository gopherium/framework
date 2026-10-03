// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gopherium/gouncer"
)

func TestReachRefusesTheFirstCapabilityTheActingRoleLacks(t *testing.T) {
	t.Parallel()

	manager := reach{
		actor: gouncer.User{Email: "manager@example.com", Role: "manager"},
		carried: map[string][]string{
			"admin":   {"manage_users", "change_others_work", "publish_reports"},
			"editor":  {"change_others_work"},
			"manager": {"manage_users"},
		},
	}
	editor := gouncer.User{Email: "editor@example.com", Role: "editor"}

	tests := []struct {
		name string
		got  error
		want error
	}{
		{"a role carrying more", manager.gives("admin"),
			errors.New("the role admin carries change_others_work, which the account manager@example.com lacks")},
		{"an account under a role carrying more", manager.changes(editor), errors.New(
			"the role editor of editor@example.com carries change_others_work, which the account manager@example.com lacks")},
		{"the role the acting account holds", manager.gives("manager"), nil},
		{"a role the capabilities leave out", manager.gives("author"), nil},
		{"the zero value giving a role", reach{}.gives("admin"), nil},
		{"the zero value changing an account", reach{}.changes(editor), nil},
	}
	for _, tt := range tests {
		if fmt.Sprint(tt.got) != fmt.Sprint(tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}
