package datasources

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// deviceRegistryWS is a WSCommander that answers `config/device_registry/list` with its JSON.
type deviceRegistryWS string

func (ws deviceRegistryWS) Command(_ context.Context, typ string, _ map[string]any, result any) error {
	if typ != "config/device_registry/list" {
		return fmt.Errorf("unexpected command %q", typ)
	}
	return json.Unmarshal([]byte(ws), result)
}

// Two devices as HA lists them: one with identifiers and connections (one with a numeric
// component), one without either key.
const registryDevices = deviceRegistryWS(`[
	{"id": "d1", "name": "Plug", "labels": [], "config_entries": ["E1"],
	 "identifiers": [["mqtt", "zigbee2mqtt_0x01"]], "connections": [["zigbee", 42]]},
	{"id": "d2", "name": "Moon", "labels": [], "config_entries": ["E2"]}
]`)

// readDataSource runs ds's Read with the config attrs (the others null) and returns the state.
func readDataSource(t *testing.T, ds datasource.DataSource, attrs map[string]tftypes.Value) tfsdk.State {
	t.Helper()
	ctx := t.Context()
	var s datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &s)
	typ := s.Schema.Type().TerraformType(ctx).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, at := range typ.AttributeTypes {
		vals[name] = tftypes.NewValue(at, nil)
	}
	for name, v := range attrs {
		vals[name] = v
	}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: s.Schema, Raw: tftypes.NewValue(typ, nil)}}
	ds.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s.Schema, Raw: tftypes.NewValue(typ, vals)}}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	return resp.State
}

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
