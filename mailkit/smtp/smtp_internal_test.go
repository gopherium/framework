// SPDX-License-Identifier: Apache-2.0

package smtp

import (
	"testing"

	gomail "github.com/wneessen/go-mail"
)

func TestPortAnswersTheSubmissionDefault(t *testing.T) {
	t.Parallel()

	if held := port(0); held != 587 {
		t.Errorf("port(0) = %d, want the submission port 587", held)
	}
}

func TestPortKeepsAConfiguredPort(t *testing.T) {
	t.Parallel()

	if held := port(2525); held != 2525 {
		t.Errorf("port(2525) = %d, want the configured port kept", held)
	}
}

func TestPolicyOfMapsEveryNamedPolicy(t *testing.T) {
	t.Parallel()

	tests := map[TLS]gomail.TLSPolicy{
		TLSMandatory:     gomail.TLSMandatory,
		"":               gomail.TLSMandatory,
		TLSOpportunistic: gomail.TLSOpportunistic,
		TLSNone:          gomail.NoTLS,
	}
	for named, want := range tests {
		held, err := policyOf(named)
		if err != nil {
			t.Errorf("policyOf(%q) error = %v, want nil", named, err)
			continue
		}
		if held != want {
			t.Errorf("policyOf(%q) = %v, want %v", named, held, want)
		}
	}
}

func TestPolicyOfRefusesAnUnknownPolicy(t *testing.T) {
	t.Parallel()

	if _, err := policyOf("sometimes"); err == nil {
		t.Error("policyOf() error = nil, want an unknown policy refused")
	}
}
