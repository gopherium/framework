// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// wipTag selects the scenarios no code makes pass yet.
const wipTag = "@wip"

func TestFeatures(t *testing.T) {
	tags := "~" + wipTag
	if os.Getenv("GOSPA_BDD_WIP") != "" {
		tags = ""
	}
	suite := godog.TestSuite{
		ScenarioInitializer: func(*godog.ScenarioContext) {},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			Tags:     tags,
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Error("the features did not pass")
	}
}
