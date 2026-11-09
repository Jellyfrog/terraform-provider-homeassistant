package datasources_test

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/toelke/terraform-provider-homeassistant/internal/acctest"
	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// registryFixture is what TestAccAreaAndDeviceDataSources sets up through the API.
type registryFixture struct {
	areaID, labelID, deviceID, entryID string
}

// setUpRegistry creates an area, a label, and a config entry of moon, which needs no hardware
// and brings one service device named "Moon". The device is put in the area, gets the label, and
// is renamed to "Acc Moon".
func setUpRegistry(t *testing.T) registryFixture {
	t.Helper()
	ha := acctest.SharedInstance(t)
	u, err := url.Parse(ha.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := client.New(client.Config{URL: u, Token: ha.Token, Timeout: 30 * time.Second})
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	areas := client.Areas(c.WS)
	area, err := areas.Create(ctx, map[string]any{"name": "Acc Reg Garden"})
	must(err)
	t.Cleanup(func() { _ = areas.Delete(context.Background(), area.AreaID) })

	labels := client.Labels(c.WS)
	label, err := labels.Create(ctx, map[string]any{"name": "Acc Reg Label"})
	must(err)
	t.Cleanup(func() { _ = labels.Delete(context.Background(), label.LabelID) })

	entries := client.NewConfigEntries(c.WS, c.REST)
	entryID, err := client.NewConfigFlows(c.REST).Run(ctx, "moon", "", []client.FlowStep{{StepID: "user"}})
	must(err)
	t.Cleanup(func() { _, _ = entries.Delete(context.Background(), entryID) })

	// The device appears once the entry has set up its sensor, shortly after the flow ends.
	devices := client.Devices(c.WS)
	var device client.Device
	acctest.Eventually(t, 30*time.Second, "a device of config entry "+entryID, func() (bool, error) {
		list, err := devices.List(ctx)
		for _, d := range list {
			if slices.Contains(d.ConfigEntries, entryID) {
				device = d
			}
		}
		return device.ID != "", err
	})
	_, err = devices.Update(ctx, device.ID, map[string]any{
		"area_id": area.AreaID, "labels": []string{label.LabelID}, "name_by_user": "Acc Moon",
	})
	must(err)

	return registryFixture{areaID: area.AreaID, labelID: label.LabelID, deviceID: device.ID, entryID: entryID}
}

func TestAccAreaAndDeviceDataSources(t *testing.T) {
	f := setUpRegistry(t)
	// The valid step comes last: the post-test destroy runs with the last step's config, which
	// fails if that config does not validate or read.
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: acctest.ProviderConfig + `
data "homeassistant_area" "missing" {
  name = "Acc Reg No Such Area"
}
`,
				ExpectError: regexp.MustCompile(`no area has name "Acc Reg No Such Area"`),
			},
			{
				Config: acctest.ProviderConfig + `
data "homeassistant_device" "missing" {
  device_id = "no-such-device"
}
`,
				ExpectError: regexp.MustCompile(`no device has ID "no-such-device"`),
			},
			{
				Config: acctest.ProviderConfig + fmt.Sprintf(`
data "homeassistant_area" "both" {
  area_id = %q
  name    = "Acc Reg Garden"
}
`, f.areaID),
				ExpectError: regexp.MustCompile("Set exactly one of `area_id` and `name`"),
			},
			{
				Config: acctest.ProviderConfig + `
data "homeassistant_device" "neither" {}
`,
				ExpectError: regexp.MustCompile("Set exactly one of `device_id`, `name` and `identifier`"),
			},
			{
				Config: acctest.ProviderConfig + fmt.Sprintf(`
data "homeassistant_areas" "all" {}

data "homeassistant_area" "by_id" {
  area_id = %[1]q
}

data "homeassistant_area" "by_name" {
  name = "Acc Reg Garden"
}

data "homeassistant_devices" "moon" {
  integration = "moon"
}

data "homeassistant_devices" "in_area_with_label" {
  area_id = %[1]q
  label   = %[2]q
}

data "homeassistant_devices" "unknown_integration" {
  integration = "no_such_integration"
}

data "homeassistant_devices" "unknown_manufacturer" {
  manufacturer = "No Such Manufacturer"
}

data "homeassistant_device" "by_id" {
  device_id = %[3]q
}

data "homeassistant_device" "by_name_by_user" {
  name = "Acc Moon"
}

data "homeassistant_device" "by_integration_name" {
  name = "Moon"
}

data "homeassistant_device" "by_identifier" {
  identifier = data.homeassistant_devices.moon.devices[0].identifiers[0]
}

output "area_listed" {
  value = contains([for a in data.homeassistant_areas.all.areas : a.area_id], %[1]q)
}
`, f.areaID, f.labelID, f.deviceID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("area_listed", "true"),
					resource.TestCheckResourceAttr("data.homeassistant_area.by_id", "name", "Acc Reg Garden"),
					resource.TestCheckResourceAttr("data.homeassistant_area.by_id", "aliases.#", "0"),
					resource.TestCheckNoResourceAttr("data.homeassistant_area.by_id", "floor_id"),
					resource.TestCheckResourceAttr("data.homeassistant_area.by_name", "area_id", f.areaID),

					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.#", "1"),
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.device_id", f.deviceID),
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.name", "Moon"),
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.name_by_user", "Acc Moon"),
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.area_id", f.areaID),
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.labels.0", f.labelID),
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.config_entries.0", f.entryID),
					resource.TestCheckNoResourceAttr("data.homeassistant_devices.moon", "devices.0.manufacturer"),
					resource.TestCheckResourceAttr("data.homeassistant_devices.in_area_with_label", "devices.#", "1"),
					resource.TestCheckResourceAttr("data.homeassistant_devices.in_area_with_label", "devices.0.device_id", f.deviceID),
					resource.TestCheckResourceAttr("data.homeassistant_devices.unknown_integration", "devices.#", "0"),
					resource.TestCheckResourceAttr("data.homeassistant_devices.unknown_manufacturer", "devices.#", "0"),

					resource.TestCheckResourceAttr("data.homeassistant_device.by_id", "name", "Moon"),
					resource.TestCheckResourceAttr("data.homeassistant_device.by_id", "name_by_user", "Acc Moon"),
					resource.TestCheckResourceAttr("data.homeassistant_device.by_name_by_user", "device_id", f.deviceID),
					resource.TestCheckResourceAttr("data.homeassistant_device.by_name_by_user", "name", "Acc Moon"),
					resource.TestCheckResourceAttr("data.homeassistant_device.by_integration_name", "device_id", f.deviceID),
					// moon identifies its device by its config entry.
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.identifiers.#", "1"),
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.identifiers.0.0", "moon"),
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.identifiers.0.1", f.entryID),
					resource.TestCheckResourceAttr("data.homeassistant_devices.moon", "devices.0.connections.#", "0"),
					resource.TestCheckResourceAttr("data.homeassistant_device.by_identifier", "device_id", f.deviceID),
					resource.TestCheckResourceAttr("data.homeassistant_device.by_identifier", "identifier.0", "moon"),
					resource.TestCheckResourceAttr("data.homeassistant_device.by_identifier", "identifier.1", f.entryID),
					resource.TestCheckResourceAttr("data.homeassistant_device.by_id", "identifiers.0.0", "moon"),
				),
			},
		},
	})
}
