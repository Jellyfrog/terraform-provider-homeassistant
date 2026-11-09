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
// disabled, two are hidden, and one has a numeric unique ID, as HA keeps it for integrations
// that reported one.
const testEntityRegistry = `[
	{"id": "r1", "entity_id": "sensor.plug_rssi", "unique_id": "0x01_rssi", "platform": "mqtt",
	 "device_id": "dev_plug", "entity_category": "diagnostic", "disabled_by": "integration", "hidden_by": null},
	{"id": "r2", "entity_id": "switch.plug", "unique_id": 1001, "platform": "mqtt",
	 "device_id": "dev_plug", "entity_category": null, "disabled_by": null, "hidden_by": null},
	{"id": "r3", "entity_id": "switch.plug_child_lock", "unique_id": "0x01_lock", "platform": "mqtt",
	 "device_id": "dev_plug", "entity_category": "config", "disabled_by": null, "hidden_by": "user"},
	{"id": "r4", "entity_id": "update.hacs", "unique_id": "hacs", "platform": "hacs",
	 "device_id": null, "entity_category": "config", "disabled_by": null, "hidden_by": null},
	{"id": "r5", "entity_id": "light.old_lamp", "unique_id": "lamp", "platform": "mqtt",
	 "device_id": null, "entity_category": null, "disabled_by": null, "hidden_by": "user"}
]`

// registryEntities answers `config/entity_registry/list` with testEntityRegistry.
var registryEntities = registryWS{typ: "config/entity_registry/list", json: testEntityRegistry}

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
		"device":               {entityRegistryFilter{deviceID: str("dev_plug")}, []string{"sensor.plug_rssi", "switch.plug", "switch.plug_child_lock"}},
		"unknown device":       {entityRegistryFilter{deviceID: str("dev_none")}, nil},
		"domain is a prefix":   {entityRegistryFilter{domain: str("switch")}, []string{"switch.plug", "switch.plug_child_lock"}},
		"domain is whole":      {entityRegistryFilter{domain: str("switc")}, nil},
		"platform":             {entityRegistryFilter{platform: str("hacs")}, []string{"update.hacs"}},
		"category":             {entityRegistryFilter{entityCategory: str("config")}, []string{"switch.plug_child_lock", "update.hacs"}},
		"device and category":  {entityRegistryFilter{deviceID: str("dev_plug"), entityCategory: str("diagnostic")}, []string{"sensor.plug_rssi"}},
		"domain and platform":  {entityRegistryFilter{domain: str("light"), platform: str("mqtt")}, []string{"light.old_lamp"}},
		"every filter, no hit": {entityRegistryFilter{deviceID: str("dev_plug"), domain: str("update"), platform: str("mqtt"), entityCategory: str("config")}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if got := entryIDs(tc.filter.apply(entries)); !slices.Equal(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEntityRegistryRead reads the plug's entities end to end against a fake WebSocket.
func TestEntityRegistryRead(t *testing.T) {
	ds := &entityRegistryDataSource{registry: client.Entities(registryEntities)}
	state := readDataSource(t, ds, map[string]tftypes.Value{"device_id": tftypes.NewValue(tftypes.String, "dev_plug")})

	var got entityRegistryModel
	if diags := state.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	s, null := types.StringValue, types.StringNull()
	want := []entityRegistryEntry{
		{
			ID: s("r1"), EntityID: s("sensor.plug_rssi"), UniqueID: s("0x01_rssi"), Platform: s("mqtt"),
			DeviceID: s("dev_plug"), EntityCategory: s("diagnostic"), DisabledBy: s("integration"), HiddenBy: null,
		},
		{
			ID: s("r2"), EntityID: s("switch.plug"), UniqueID: s("1001"), Platform: s("mqtt"),
			DeviceID: s("dev_plug"), EntityCategory: null, DisabledBy: null, HiddenBy: null,
		},
		{
			ID: s("r3"), EntityID: s("switch.plug_child_lock"), UniqueID: s("0x01_lock"), Platform: s("mqtt"),
			DeviceID: s("dev_plug"), EntityCategory: s("config"), DisabledBy: null, HiddenBy: s("user"),
		},
	}
	if !slices.Equal(got.Entities, want) {
		t.Errorf("entities = %v\nwant %v", got.Entities, want)
	}
	if got.DeviceID.ValueString() != "dev_plug" || !got.Domain.IsNull() {
		t.Errorf("filters in state = %v, %v", got.DeviceID, got.Domain)
	}
}

// TestEntityRegistryReadError checks that a failing registry list fails the read.
func TestEntityRegistryReadError(t *testing.T) {
	failing := registryEntities
	failing.err = errors.New("connection refused")
	registry := client.Entities(failing)
	resp := runRead(t, &entityRegistryDataSource{registry: registry}, nil)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Read succeeded, want an error")
	}
	if got := resp.Diagnostics.Errors()[0]; got.Summary() != "Reading the entity registry" || !strings.Contains(got.Detail(), "connection refused") {
		t.Errorf("error = %q: %q", got.Summary(), got.Detail())
	}
}
