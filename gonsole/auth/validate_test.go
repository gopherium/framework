// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"testing"
	"time"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

func TestValidateReadsEveryAccountSetting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      auth.Config
		settings map[string]string
		want     string
	}{
		{"the fallbacks", auth.Config{RecordTimeout: time.Second, RecordsLimit: 50}, nil, ""},
		{"settings above zero", auth.Config{},
			map[string]string{"MYAPP_COMMAND_RECORD_TIMEOUT": "2s", "MYAPP_COMMAND_RECORDS_LIMIT": "10"}, ""},
		{"a malformed record timeout", auth.Config{RecordTimeout: time.Second, RecordsLimit: 50},
			map[string]string{"MYAPP_COMMAND_RECORD_TIMEOUT": "soon"},
			`MYAPP_COMMAND_RECORD_TIMEOUT: must be a duration like 30s, got "soon"`},
		{"a malformed records limit", auth.Config{RecordTimeout: time.Second, RecordsLimit: 50},
			map[string]string{"MYAPP_COMMAND_RECORDS_LIMIT": "all"},
			`MYAPP_COMMAND_RECORDS_LIMIT: must be a whole number, got "all"`},
		{"fallbacks not above zero", auth.Config{}, nil,
			"gonsole/auth: the record timeout must stand above zero, got 0s\n" +
				"gonsole/auth: the records limit must stand above zero, got 0"},
		{"fallbacks below zero", auth.Config{RecordTimeout: -time.Second, RecordsLimit: -1}, nil,
			"gonsole/auth: the record timeout must stand above zero, got -1s\n" +
				"gonsole/auth: the records limit must stand above zero, got -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate(gonsole.Env{Prefix: "MYAPP_", Getenv: testkit.Getenv(tt.settings)})

			if got := errorText(err); got != tt.want {
				t.Errorf("Validate() = %q, want %q", got, tt.want)
			}
		})
	}
}
