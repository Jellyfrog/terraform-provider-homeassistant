package datasources_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/toelke/terraform-provider-homeassistant/internal/acctest"
)

// TestAccEntityRegistryDataSource reads two MQTT sensors of its own from the entity registry: the
// first is a primary entity, the second a diagnostic one. Then it disables the first sensor,
// which drops it from /api/states but not from the registry.
func TestAccEntityRegistryDataSource(t *testing.T) {
	deviceIDs, entityIDs := setUpMQTTSensors(t,
		mqttSensor{id: "acc_registry_primary", device: "Acc Registry Primary"},
		mqttSensor{id: "acc_registry_diagnostic", device: "Acc Registry Diagnostic", category: "diagnostic"},
	)
	// Three reads: one unfiltered, which the locals work from, and two that run the filters in
	// the provider. Every filter is scoped to one of this test's devices, so entities that other
	// tests leave on the shared instance don't change the counts.
	config := acctest.ProviderConfig + fmt.Sprintf(`
data "homeassistant_entity_registry" "all" {}

data "homeassistant_entity_registry" "first" {
  device_id = %[1]q
}

data "homeassistant_entity_registry" "second" {
  device_id       = %[2]q
  domain          = "sensor"
  platform        = "mqtt"
  entity_category = "diagnostic"
}

locals {
  # The use case of issue #90: each device's primary entities, from one read.
  primary = {
    for e in data.homeassistant_entity_registry.all.entities :
    e.device_id => e.entity_id... if e.device_id != null && e.entity_category == null
    && e.hidden_by == null && e.disabled_by == null
  }
}

output "first_primary" {
  value = jsonencode(lookup(local.primary, %[1]q, []))
}

output "second_has_primary" {
  value = contains(keys(local.primary), %[2]q)
}
`, deviceIDs[0], deviceIDs[1])
	first := "data.homeassistant_entity_registry.first"
	second := "data.homeassistant_entity_registry.second"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(first, "entities.#", "1"),
					resource.TestCheckResourceAttr(first, "entities.0.entity_id", entityIDs[0]),
					resource.TestCheckResourceAttr(first, "entities.0.unique_id", "acc_registry_primary_temperature"),
					resource.TestCheckResourceAttr(first, "entities.0.platform", "mqtt"),
					resource.TestCheckResourceAttr(first, "entities.0.device_id", deviceIDs[0]),
					resource.TestCheckNoResourceAttr(first, "entities.0.entity_category"),
					resource.TestCheckNoResourceAttr(first, "entities.0.disabled_by"),
					resource.TestCheckNoResourceAttr(first, "entities.0.hidden_by"),
					resource.TestCheckResourceAttrSet(first, "entities.0.id"),
					resource.TestCheckResourceAttr(second, "entities.#", "1"),
					resource.TestCheckResourceAttr(second, "entities.0.entity_id", entityIDs[1]),
					resource.TestCheckResourceAttr(second, "entities.0.entity_category", "diagnostic"),
					resource.TestCheckOutput("first_primary", fmt.Sprintf("[%q]", entityIDs[0])),
					resource.TestCheckOutput("second_has_primary", "false"),
				),
			},
			{
				PreConfig: func() {
					c := entitiesClient(t)
					updateEntity(t, c, map[string]any{"entity_id": entityIDs[0], "disabled_by": "user"})
					// Re-enable it, so a rerun against the same instance doesn't find it disabled.
					t.Cleanup(func() {
						updateEntity(t, c, map[string]any{"entity_id": entityIDs[0], "disabled_by": nil})
					})
				},
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(first, "entities.#", "1"),
					resource.TestCheckResourceAttr(first, "entities.0.entity_id", entityIDs[0]),
					resource.TestCheckResourceAttr(first, "entities.0.disabled_by", "user"),
					resource.TestCheckOutput("first_primary", "[]"),
				),
			},
		},
	})
}
