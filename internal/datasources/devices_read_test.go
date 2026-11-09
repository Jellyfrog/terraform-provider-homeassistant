package datasources

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// Two devices as HA lists them: one with identifiers and connections (one with a numeric
// component), one without either key.
var registryDevices = registryWS{typ: "config/device_registry/list", json: `[
	{"id": "d1", "name": "Plug", "labels": [], "config_entries": ["E1"],
	 "identifiers": [["mqtt", "zigbee2mqtt_0x01"]], "connections": [["zigbee", 42]]},
	{"id": "d2", "name": "Moon", "labels": [], "config_entries": ["E2"]}
]`}

func TestDevicesRead(t *testing.T) {
	r := client.Devices(registryDevices)
	state := readDataSource(t, &devicesDataSource{devices: &r}, nil)

	var got devicesModel
	if diags := state.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("devices = %+v", got.Devices)
	}
	plug, moon := got.Devices[0], got.Devices[1]
	if s := plug.Identifiers.String(); s != `[["mqtt","zigbee2mqtt_0x01"]]` {
		t.Errorf("d1 identifiers = %s", s)
	}
	if s := plug.Connections.String(); s != `[["zigbee","42"]]` {
		t.Errorf("d1 connections = %s", s)
	}
	if s := moon.Identifiers.String() + moon.Connections.String(); s != `[][]` {
		t.Errorf("d2 identifiers, connections = %s; want empty lists", s)
	}
}

func TestDeviceReadByIdentifier(t *testing.T) {
	r := client.Devices(registryDevices)
	identifier := tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{
		tftypes.NewValue(tftypes.String, "mqtt"), tftypes.NewValue(tftypes.String, "zigbee2mqtt_0x01"),
	})
	state := readDataSource(t, &deviceDataSource{devices: &r}, map[string]tftypes.Value{"identifier": identifier})

	var got deviceLookupModel
	if diags := state.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	if got.DeviceID.ValueString() != "d1" || got.Name.ValueString() != "Plug" ||
		got.Connections.String() != `[["zigbee","42"]]` || len(got.Identifier) != 2 {
		t.Errorf("device = %+v", got)
	}
}
