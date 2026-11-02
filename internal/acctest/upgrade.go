package acctest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// ReleasedVersion is the version constraint of the released provider that upgrade tests start
// from: the latest release of the current major version. The first release of a new major
// version updates it.
const ReleasedVersion = "~> 1.0"

// releasedProviderConfig is ProviderConfig with the released provider's version constraint.
var releasedProviderConfig = fmt.Sprintf(`
terraform {
  required_providers {
    homeassistant = {
      source  = "toelke/homeassistant"
      version = %q
    }
  }
}
`, ReleasedVersion)

// UpgradeTest checks that state written by the latest release of the current major version
// works with the build under test. It applies config with the released provider from the
// registry, then plans the same config with the build under test: the plan must be empty, and
// the checks must hold on the refreshed state. config starts with ProviderConfig, like every
// test configuration.
//
// A resource whose state changes on purpose checks the new value in checks. One that cannot plan
// empty after an upgrade needs a state upgrader or a new major version.
func UpgradeTest(t *testing.T, config string, checks ...resource.TestCheckFunc) {
	t.Helper()
	body, ok := strings.CutPrefix(config, ProviderConfig)
	if !ok {
		t.Fatal("acctest.UpgradeTest: config must start with acctest.ProviderConfig")
	}
	resource.Test(t, resource.TestCase{
		PreCheck: func() { PreCheck(t) },
		// The two steps use different providers, so they are set per step.
		Steps: []resource.TestStep{
			{
				ExternalProviders: map[string]resource.ExternalProvider{
					"homeassistant": {Source: "toelke/homeassistant", VersionConstraint: ReleasedVersion},
				},
				// The configuration has a terraform block, so the version constraint must be in it:
				// terraform-plugin-testing adds none from ExternalProviders.
				Config: releasedProviderConfig + body,
			},
			{
				ProtoV6ProviderFactories: ProtoV6ProviderFactories,
				Config:                   config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(checks...),
			},
		},
	})
}
