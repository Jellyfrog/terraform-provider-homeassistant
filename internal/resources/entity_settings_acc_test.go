package resources_test

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/toelke/terraform-provider-homeassistant/internal/acctest"
	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// moonEntity creates a config entry of moon, which needs no hardware, and returns the entity ID
// of its sensor once that is in the entity registry. The entry is deleted when the test ends.
func moonEntity(t *testing.T) string {
	t.Helper()
	c := haClient(t)
	ctx := context.Background()
	entryID, err := client.NewConfigFlows(c.REST).Run(ctx, "moon", "", []client.FlowStep{{StepID: "user"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = client.NewConfigEntries(c.WS, c.REST).Delete(context.Background(), entryID) })

	entities := client.Entities(c.WS)
	var entityID string
	acctest.Eventually(t, 30*time.Second, "the moon sensor in the entity registry", func() (bool, error) {
		list, err := entities.List(ctx)
		if i := slices.IndexFunc(list, func(e client.EntityEntry) bool { return e.Platform == "moon" }); i >= 0 {
			entityID = list[i].EntityID
		}
		return entityID != "", err
	})
	return entityID
}

// checkEntityEntry runs check on the registry entry of entityID, which must exist.
func checkEntityEntry(t *testing.T, entityID string, check func(client.EntityEntry) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		e, ok, err := client.Entities(haClient(t).WS).Get(context.Background(), entityID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("entity %s is gone", entityID)
		}
		return check(e)
	}
}

func deref(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

func entitySettingsConfig(entityID, settings string) string {
	return acctest.ProviderConfig + fmt.Sprintf(`
resource "homeassistant_area" "sky" {
  name = "Acc Sky"
}

resource "homeassistant_label" "celestial" {
  name = "Acc Celestial"
}

resource "homeassistant_entity_settings" "moon" {
  entity_id = %q
%s
}
`, entityID, settings)
}

func TestAccEntitySettings(t *testing.T) {
	const addr = "homeassistant_entity_settings.moon"
	entityID := moonEntity(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		// Destroy resets only the configured fields and leaves the entity, and the icon set
		// outside Tofu, in place.
		CheckDestroy: checkEntityEntry(t, entityID, func(e client.EntityEntry) error {
			if e.Name != nil || e.AreaID != nil || len(e.Labels) != 0 || e.HiddenBy != nil || e.DisabledBy != nil {
				return fmt.Errorf("not reset: name %s, area %s, labels %v, hidden_by %s, disabled_by %s",
					deref(e.Name), deref(e.AreaID), e.Labels, deref(e.HiddenBy), deref(e.DisabledBy))
			}
			if deref(e.Icon) != "mdi:moon-full" {
				return fmt.Errorf("icon = %s, want the unmanaged mdi:moon-full kept", deref(e.Icon))
			}
			return nil
		}),
		Steps: []resource.TestStep{
			{
				Config: entitySettingsConfig(entityID, `
  name     = "Acc Moon Phase"
  area_id  = homeassistant_area.sky.id
  labels   = [homeassistant_label.celestial.id]
  hidden   = true
  disabled = false
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", "Acc Moon Phase"),
					resource.TestCheckResourceAttr(addr, "area_id", "acc_sky"),
					resource.TestCheckResourceAttr(addr, "labels.#", "1"),
					resource.TestCheckResourceAttr(addr, "hidden", "true"),
					resource.TestCheckResourceAttr(addr, "disabled", "false"),
					resource.TestCheckResourceAttr(addr, "platform", "moon"),
					resource.TestCheckResourceAttrSet(addr, "device_id"),
					resource.TestCheckNoResourceAttr(addr, "icon"),
					checkEntityEntry(t, entityID, func(e client.EntityEntry) error {
						if deref(e.Name) != "Acc Moon Phase" || deref(e.AreaID) != "acc_sky" ||
							!slices.Equal(e.Labels, []string{"acc_celestial"}) || deref(e.HiddenBy) != "user" ||
							e.DisabledBy != nil {
							return fmt.Errorf("entry = %+v", e)
						}
						return nil
					}),
				),
			},
			{
				// An attribute changed in the UI that is not in config causes no diff.
				PreConfig: func() {
					_, err := client.Entities(haClient(t).WS).Update(context.Background(), entityID,
						map[string]any{"icon": "mdi:moon-full"})
					if err != nil {
						t.Fatal(err)
					}
				},
				Config: entitySettingsConfig(entityID, `
  name     = "Acc Moon Phase"
  area_id  = homeassistant_area.sky.id
  labels   = [homeassistant_label.celestial.id]
  hidden   = true
  disabled = false
`),
				PlanOnly: true,
			},
			{
				// A configured attribute changed in the UI is drift.
				PreConfig: func() {
					_, err := client.Entities(haClient(t).WS).Update(context.Background(), entityID,
						map[string]any{"name": "Changed in the UI"})
					if err != nil {
						t.Fatal(err)
					}
				},
				Config: entitySettingsConfig(entityID, `
  name     = "Acc Moon Phase"
  area_id  = homeassistant_area.sky.id
  labels   = [homeassistant_label.celestial.id]
  hidden   = true
  disabled = false
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: checkEntityEntry(t, entityID, func(e client.EntityEntry) error {
					if deref(e.Name) != "Acc Moon Phase" {
						return fmt.Errorf("name = %s, want it restored", deref(e.Name))
					}
					return nil
				}),
			},
			{
				// Import manages nothing until the configuration says so.
				ResourceName:                         addr,
				ImportState:                          true,
				ImportStateId:                        entityID,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "entity_id",
				ImportStateVerifyIgnore:              []string{"name", "area_id", "labels", "hidden", "disabled"},
			},
			{
				// Dropping name resets it; hidden and disabled map to null and "user".
				Config: entitySettingsConfig(entityID, `
  area_id  = homeassistant_area.sky.id
  labels   = [homeassistant_label.celestial.id]
  hidden   = false
  disabled = true
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(addr, "name"),
					resource.TestCheckResourceAttr(addr, "hidden", "false"),
					resource.TestCheckResourceAttr(addr, "disabled", "true"),
					checkEntityEntry(t, entityID, func(e client.EntityEntry) error {
						if e.Name != nil || e.HiddenBy != nil || deref(e.DisabledBy) != "user" {
							return fmt.Errorf("entry = %+v", e)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccEntitySettings_Timeout(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: acctest.ProviderConfig + `
resource "homeassistant_entity_settings" "missing" {
  entity_id = "sensor.acc_no_such_entity"
  name      = "Never"
  timeouts = {
    create = "2s"
  }
}
`,
				ExpectError: regexp.MustCompile(`did\s+not\s+appear\s+in\s+the\s+entity\s+registry\s+within\s+2s`),
			},
		},
	})
}

// renameEntity renames the entity from to to, as the entity settings dialog does.
func renameEntity(t *testing.T, from, to string) {
	t.Helper()
	_, err := client.Entities(haClient(t).WS).Update(context.Background(), from, map[string]any{"new_entity_id": to})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAccEntitySettings_Rename(t *testing.T) {
	const addr = "homeassistant_entity_settings.moon"
	entityID := moonEntity(t)
	renamed, final := "sensor.acc_moon_renamed", "sensor.acc_moon_final"
	// An entity that holds an entity ID a rename cannot take.
	ctx := context.Background()
	taken, err := inputBooleans(t).Create(ctx, map[string]any{"name": "Acc Taken"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inputBooleans(t).Delete(ctx, taken.HelperID()) })

	var registryID string
	settings := `
  name = "Acc Moon Phase"
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		// Destroy resets the name on the renamed entity and does not rename it back.
		CheckDestroy: checkEntityEntry(t, final, func(e client.EntityEntry) error {
			if e.Name != nil {
				return fmt.Errorf("name = %s, want it reset", deref(e.Name))
			}
			return nil
		}),
		Steps: []resource.TestStep{
			{
				Config: entitySettingsConfig(entityID, settings),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error {
						registryID = v
						if v == "" {
							return fmt.Errorf("id is empty")
						}
						return nil
					}),
				),
			},
			{
				// Renamed in the UI: the plan renames it back in place.
				PreConfig: func() { renameEntity(t, entityID, renamed) },
				Config:    entitySettingsConfig(entityID, settings),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "entity_id", entityID),
					checkEntityEntry(t, entityID, func(e client.EntityEntry) error {
						if e.ID != registryID || deref(e.Name) != "Acc Moon Phase" {
							return fmt.Errorf("entry = %+v, want id %s renamed back", e, registryID)
						}
						return nil
					}),
				),
			},
			{
				// Renamed in the UI and in config: nothing to do.
				PreConfig: func() { renameEntity(t, entityID, renamed) },
				Config:    entitySettingsConfig(renamed, settings),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Renamed in config: an in-place update keeps the registry ID and the settings.
				Config: entitySettingsConfig(final, settings),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "entity_id", final),
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error {
						if v != registryID {
							return fmt.Errorf("id = %s, want %s", v, registryID)
						}
						return nil
					}),
					checkEntityEntry(t, final, func(e client.EntityEntry) error {
						if e.ID != registryID || deref(e.Name) != "Acc Moon Phase" {
							return fmt.Errorf("entry = %+v", e)
						}
						return nil
					}),
				),
			},
			{
				// Another entity has the entity ID: HA's error, and the hint to replace instead.
				Config:      entitySettingsConfig("input_boolean.acc_taken", settings),
				ExpectError: regexp.MustCompile(`(?s)input_boolean\.acc_taken.*tofu\s+apply\s+-replace`),
			},
			{
				// The failed rename left the state alone.
				Config: entitySettingsConfig(final, settings),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Import by entity ID fills in the registry ID.
				ResourceName:                         addr,
				ImportState:                          true,
				ImportStateId:                        final,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "entity_id",
				ImportStateVerifyIgnore:              []string{"name"},
			},
		},
	})
}

// State written by v1.0.0, before the registry ID (ADR-0025), plans empty and gets the ID.
func TestAccEntitySettings_UpgradeFromV1(t *testing.T) {
	const addr = "homeassistant_entity_settings.moon"
	entityID := moonEntity(t)
	settings := `
  name = "Acc Moon Phase"
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() { acctest.PreCheck(t) },
		Steps: []resource.TestStep{
			{
				ExternalProviders: map[string]resource.ExternalProvider{
					"homeassistant": {Source: "toelke/homeassistant", VersionConstraint: "1.0.0"},
				},
				Config: strings.Replace(entitySettingsConfig(entityID, settings),
					`source = "toelke/homeassistant"`, `source  = "toelke/homeassistant"
      version = "1.0.0"`, 1),
			},
			{
				ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
				Config:                   entitySettingsConfig(entityID, settings),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttrSet(addr, "id"),
			},
		},
	})
}
