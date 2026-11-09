package datasources

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// testEntityRegistry is the entity registry as HA lists it: entries with a device and a
// category, with a device only, with a category only, and with neither; one of them is
// disabled and one hidden.
const testEntityRegistry = `[
	{"id": "r1", "entity_id": "sensor.plug_rssi", "unique_id": "0x01_rssi", "platform": "mqtt",
	 "device_id": "dev_plug", "entity_category": "diagnostic", "disabled_by": "integration", "hidden_by": null},
	{"id": "r2", "entity_id": "switch.plug", "unique_id": "0x01_switch", "platform": "mqtt",
	 "device_id": "dev_plug", "entity_category": null, "disabled_by": null, "hidden_by": null},
	{"id": "r3", "entity_id": "switch.plug_child_lock", "unique_id": "0x01_lock", "platform": "mqtt",
	 "device_id": "dev_plug", "entity_category": "config", "disabled_by": null, "hidden_by": null},
	{"id": "r4", "entity_id": "update.hacs", "unique_id": "hacs", "platform": "hacs",
	 "device_id": null, "entity_category": "config", "disabled_by": null, "hidden_by": null},
	{"id": "r5", "entity_id": "light.old_lamp", "unique_id": "lamp", "platform": "mqtt",
	 "device_id": null, "entity_category": null, "disabled_by": null, "hidden_by": "user"}
]`

func entryIDs(entries []client.EntityEntry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.EntityID
	}
	return ids
}

func TestEntityRegistryFilter(t *testing.T) {
	var entries []client.EntityEntry
	if err := json.Unmarshal([]byte(testEntityRegistry), &entries); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		filter entityRegistryFilter
		want   []string
	}{
		"none, sorted, disabled included": {entityRegistryFilter{}, []string{
			"light.old_lamp", "sensor.plug_rssi", "switch.plug", "switch.plug_child_lock", "update.hacs",
		}},
		"device":               {entityRegistryFilter{deviceID: new("dev_plug")}, []string{"sensor.plug_rssi", "switch.plug", "switch.plug_child_lock"}},
		"unknown device":       {entityRegistryFilter{deviceID: new("dev_none")}, nil},
		"domain is a prefix":   {entityRegistryFilter{domain: new("switch")}, []string{"switch.plug", "switch.plug_child_lock"}},
		"domain is whole":      {entityRegistryFilter{domain: new("switc")}, nil},
		"platform":             {entityRegistryFilter{platform: new("hacs")}, []string{"update.hacs"}},
		"category":             {entityRegistryFilter{entityCategory: new("config")}, []string{"switch.plug_child_lock", "update.hacs"}},
		"device and category":  {entityRegistryFilter{deviceID: new("dev_plug"), entityCategory: new("diagnostic")}, []string{"sensor.plug_rssi"}},
		"domain and platform":  {entityRegistryFilter{domain: new("light"), platform: new("mqtt")}, []string{"light.old_lamp"}},
		"every filter, no hit": {entityRegistryFilter{deviceID: new("dev_plug"), domain: new("update"), platform: new("mqtt"), entityCategory: new("config")}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if got := entryIDs(entityRegistryFilter.apply(tc.filter, entries)); !slices.Equal(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEntityRegistryRead reads the plug's entities end to end against a fake WebSocket.
func TestEntityRegistryRead(t *testing.T) {
	ds := &entityRegistryDataSource{registry: client.Entities(registryWS{typ: "config/entity_registry/list", json: testEntityRegistry})}
	state := readDataSource(t, ds, map[string]tftypes.Value{"device_id": tftypes.NewValue(tftypes.String, "dev_plug")})

	var got entityRegistryModel
	if diags := state.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	null := types.StringNull()
	want := []entityRegistryEntry{
		{
			ID: types.StringValue("r1"), EntityID: types.StringValue("sensor.plug_rssi"), UniqueID: types.StringValue("0x01_rssi"),
			Platform: types.StringValue("mqtt"), DeviceID: types.StringValue("dev_plug"), EntityCategory: types.StringValue("diagnostic"),
			DisabledBy: types.StringValue("integration"), HiddenBy: null,
		},
		{
			ID: types.StringValue("r2"), EntityID: types.StringValue("switch.plug"), UniqueID: types.StringValue("0x01_switch"),
			Platform: types.StringValue("mqtt"), DeviceID: types.StringValue("dev_plug"), EntityCategory: null,
			DisabledBy: null, HiddenBy: null,
		},
		{
			ID: types.StringValue("r3"), EntityID: types.StringValue("switch.plug_child_lock"), UniqueID: types.StringValue("0x01_lock"),
			Platform: types.StringValue("mqtt"), DeviceID: types.StringValue("dev_plug"), EntityCategory: types.StringValue("config"),
			DisabledBy: null, HiddenBy: null,
		},
	}
	if !slices.Equal(got.Entities, want) {
		t.Errorf("entities = %v\nwant %v", got.Entities, want)
	}
	if got.DeviceID.ValueString() != "dev_plug" || !got.Domain.IsNull() {
		t.Errorf("filters in state = %v, %v", got.DeviceID, got.Domain)
	}
}

// TestEntityRegistryReadNullsAndHidden checks the null device and category, and hidden_by.
func TestEntityRegistryReadNullsAndHidden(t *testing.T) {
	ds := &entityRegistryDataSource{registry: client.Entities(registryWS{typ: "config/entity_registry/list", json: testEntityRegistry})}
	state := readDataSource(t, ds, map[string]tftypes.Value{"domain": tftypes.NewValue(tftypes.String, "light")})

	var got entityRegistryModel
	if diags := state.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	if len(got.Entities) != 1 {
		t.Fatalf("entities = %v, want light.old_lamp only", got.Entities)
	}
	e := got.Entities[0]
	if !e.DeviceID.IsNull() || !e.EntityCategory.IsNull() || !e.DisabledBy.IsNull() || e.HiddenBy != types.StringValue("user") {
		t.Errorf("light.old_lamp = %+v", e)
	}
}

// TestEntityRegistryReadError checks that a failing registry list fails the read.
func TestEntityRegistryReadError(t *testing.T) {
	registry := client.Entities(registryWS{typ: "config/entity_registry/list", err: errors.New("connection refused")})
	resp := runRead(t, &entityRegistryDataSource{registry: registry}, nil)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Read succeeded, want an error")
	}
	if got := resp.Diagnostics.Errors()[0]; got.Summary() != "Reading the entity registry" || !strings.Contains(got.Detail(), "connection refused") {
		t.Errorf("error = %q: %q", got.Summary(), got.Detail())
	}
}
