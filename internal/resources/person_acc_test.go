package resources_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/toelke/terraform-provider-homeassistant/internal/acctest"
	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

func persons(t *testing.T) client.Persons {
	t.Helper()
	return client.NewPersons(haClient(t).WS)
}

func person(t *testing.T, id string) (client.Person, bool) {
	t.Helper()
	p, ok, _, err := persons(t).Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p, ok
}

func personConfig(body string) string {
	return acctest.ProviderConfig + fmt.Sprintf(`
resource "homeassistant_user" "jane" {
  name = "Acc Person User"
}

resource "homeassistant_person" "test" {
%s
}
`, body)
}

func TestAccPerson(t *testing.T) {
	const addr = "homeassistant_person.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if _, ok := person(t, "acc_person"); ok {
				t.Error("person acc_person still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// Home Assistant checks only the format of device trackers, so they need not exist.
				Config: personConfig(`
  name            = "Acc Person"
  user_id         = homeassistant_user.jane.id
  device_trackers = ["device_tracker.acc_tablet", "device_tracker.acc_phone"]
  picture         = "/local/acc.png"
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "id", "acc_person"),
					resource.TestCheckResourceAttrPair(addr, "user_id", "homeassistant_user.jane", "id"),
					resource.TestCheckResourceAttr(addr, "device_trackers.#", "2"),
					resource.TestCheckTypeSetElemAttr(addr, "device_trackers.*", "device_tracker.acc_phone"),
					resource.TestCheckTypeSetElemAttr(addr, "device_trackers.*", "device_tracker.acc_tablet"),
					resource.TestCheckResourceAttr(addr, "picture", "/local/acc.png"),
				),
			},
			importStep(addr, "acc_person"),
			{
				// Renamed in place, keeping the ID; the user and picture are cleared, one tracker stays.
				Config: personConfig(`
  name            = "Acc Person Renamed"
  device_trackers = ["device_tracker.acc_phone"]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					p, ok := person(t, "acc_person")
					want := client.Person{ID: "acc_person", Name: "Acc Person Renamed", DeviceTrackers: []string{"device_tracker.acc_phone"}}
					if !ok || p.Name != want.Name || p.UserID != nil || p.Picture != nil || !slices.Equal(p.DeviceTrackers, want.DeviceTrackers) {
						return fmt.Errorf("in Home Assistant: %+v, %v; want %+v", p, ok, want)
					}
					return nil
				},
			},
			{
				// Omitting device_trackers clears them; it reads back as an empty set, not as drift.
				Config: personConfig(`  name = "Acc Person Renamed"`),
				Check:  resource.TestCheckResourceAttr(addr, "device_trackers.#", "0"),
			},
		},
	})
}

func TestAccPerson_ExplicitID(t *testing.T) {
	const addr = "homeassistant_person.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Create-then-rename keeps the trackers, which an update without them would clear.
				Config: personConfig(`
  id              = "acc_pinned_person"
  name            = "Pinned Person"
  device_trackers = ["device_tracker.acc_pinned"]
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "id", "acc_pinned_person"),
					func(*terraform.State) error {
						p, ok := person(t, "acc_pinned_person")
						if !ok || p.Name != "Pinned Person" || !slices.Equal(p.DeviceTrackers, []string{"device_tracker.acc_pinned"}) {
							return fmt.Errorf("in Home Assistant: %+v, %v", p, ok)
						}
						return nil
					},
				),
			},
		},
	})
}
