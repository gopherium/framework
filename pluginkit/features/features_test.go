// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// wipTag selects the scenarios no code makes pass yet.
const wipTag = "@wip"

// runFeature runs one feature file as its own suite, refusing undefined steps.
func runFeature(t *testing.T, path string, initialize func(*godog.ScenarioContext)) {
	t.Helper()
	tags := "~" + wipTag
	if os.Getenv("PLUGINKIT_BDD_WIP") != "" {
		tags = ""
	}
	suite := godog.TestSuite{
		ScenarioInitializer: initialize,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{path},
			Tags:     tags,
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Error("the feature did not pass")
	}
}

func TestHooks(t *testing.T) {
	runFeature(t, "hooks.feature", initializeHooks(t))
}

func TestScreens(t *testing.T) {
	runFeature(t, "screens.feature", initializeScreens(t))
}

func TestRegistration(t *testing.T) {
	runFeature(t, "registration.feature", func(*godog.ScenarioContext) {})
}
