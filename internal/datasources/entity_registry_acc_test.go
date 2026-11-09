package datasources_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/toelke/terraform-provider-homeassistant/internal/acctest"
)

// TestAccEntityRegistryDataSource reads the twin devices' sensors from the entity registry: the
// first is a primary entity, the second a diagnostic one. Then it disables the first sensor,
// which drops it from /api/states but not from the registry.
func TestAccEntityRegistryDataSource(t *testing.T) {
	deviceIDs, entityIDs := setUpTwinDevices(t)
	config := acctest.ProviderConfig + fmt.Sprintf(`
data "homeassistant_entity_registry" "first" {
  device_id = %[1]q
}

data "homeassistant_entity_registry" "second" {
  device_id = %[2]q
}

data "homeassistant_entity_registry" "mqtt_diagnostic" {
  domain          = "sensor"
  platform        = "mqtt"
  entity_category = "diagnostic"
}

data "homeassistant_entity_registry" "all" {}

data "homeassistant_entity_registry" "unknown_device" {
  device_id = "acc_no_such_device"
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

output "unknown_device_count" {
  value = length(data.homeassistant_entity_registry.unknown_device.entities)
}
`, deviceIDs[0], deviceIDs[1])
	first := "data.homeassistant_entity_registry.first"
	second := "data.homeassistant_entity_registry.second"
	diagnostic := "data.homeassistant_entity_registry.mqtt_diagnostic"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(first, "entities.#", "1"),
					resource.TestCheckResourceAttr(first, "entities.0.entity_id", entityIDs[0]),
					resource.TestCheckResourceAttr(first, "entities.0.unique_id", "acc_twin_one_temperature"),
					resource.TestCheckResourceAttr(first, "entities.0.platform", "mqtt"),
					resource.TestCheckResourceAttr(first, "entities.0.device_id", deviceIDs[0]),
					resource.TestCheckNoResourceAttr(first, "entities.0.entity_category"),
					resource.TestCheckNoResourceAttr(first, "entities.0.disabled_by"),
					resource.TestCheckNoResourceAttr(first, "entities.0.hidden_by"),
					resource.TestCheckResourceAttrSet(first, "entities.0.id"),
					resource.TestCheckResourceAttr(second, "entities.#", "1"),
					resource.TestCheckResourceAttr(second, "entities.0.entity_category", "diagnostic"),
					resource.TestCheckResourceAttr(diagnostic, "entities.#", "1"),
					resource.TestCheckResourceAttr(diagnostic, "entities.0.entity_id", entityIDs[1]),
					resource.TestCheckOutput("first_primary", fmt.Sprintf("[%q]", entityIDs[0])),
					resource.TestCheckOutput("second_has_primary", "false"),
					resource.TestCheckOutput("unknown_device_count", "0"),
				),
			},
			{
				PreConfig: func() {
					c := entitiesClient(t)
					updateEntity(t, c, map[string]any{"entity_id": entityIDs[0], "disabled_by": "user"})
					// Best effort: other tests announce the same sensor again on this instance.
					t.Cleanup(func() {
						_ = c.WS.Command(context.Background(), "config/entity_registry/update",
							map[string]any{"entity_id": entityIDs[0], "disabled_by": nil}, nil)
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
