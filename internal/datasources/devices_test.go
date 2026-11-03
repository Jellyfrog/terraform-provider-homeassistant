package datasources

import (
	"slices"
	"strings"
	"testing"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

func str(s string) *string { return &s }

var testDevices = []client.Device{
	{
		ID: "d3", Name: str("Moon"), ConfigEntries: []string{"E-moon"},
		Identifiers: [][]client.RegistryString{{"mqtt", "zigbee2mqtt_0x00158d0004aaf230"}, {"moon", "7"}},
		Connections: [][]client.RegistryString{{"mac", "dc:a6:32:54:1f:88"}},
	},
	{
		ID: "d1", Name: str("Bulb"), NameByUser: str("Desk Lamp"), Manufacturer: str("Signify"),
		Model: str("LCA001"), AreaID: str("office"), Labels: []string{"managed"},
		ConfigEntries: []string{"E-hue"},
	},
	{
		ID: "d2", Name: str("Bulb"), Manufacturer: str("Signify"), Model: str("LCA002"),
		Labels: []string{"managed", "night"}, ConfigEntries: []string{"E-other", "E-hue"},
	},
	{ID: "d4", Name: str("Desk Lamp"), Manufacturer: str("IKEA"), Identifiers: [][]client.RegistryString{{"moon", "7"}}},
}

func TestDeviceFilter(t *testing.T) {
	cases := map[string]struct {
		filter deviceFilter
		want   []string
	}{
		"none":            {deviceFilter{}, []string{"d1", "d2", "d3", "d4"}},
		"manufacturer":    {deviceFilter{manufacturer: str("Signify")}, []string{"d1", "d2"}},
		"model":           {deviceFilter{model: str("LCA002")}, []string{"d2"}},
		"area":            {deviceFilter{areaID: str("office")}, []string{"d1"}},
		"label":           {deviceFilter{label: str("night")}, []string{"d2"}},
		"integration":     {deviceFilter{entries: map[string]bool{"E-hue": true}}, []string{"d1", "d2"}},
		"no entries":      {deviceFilter{entries: map[string]bool{}}, nil},
		"null never hits": {deviceFilter{manufacturer: str("")}, nil},
		"combined": {
			deviceFilter{manufacturer: str("Signify"), label: str("managed"), areaID: str("office")},
			[]string{"d1"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var got []string
			for _, d := range tc.filter.apply(testDevices) {
				got = append(got, d.ID)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("apply = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchDevice(t *testing.T) {
	cases := map[string]struct {
		id, name   *string
		identifier []string
		wantID     string
		wantErr    string
	}{
		"by identifier":         {identifier: []string{"mqtt", "zigbee2mqtt_0x00158d0004aaf230"}, wantID: "d3"},
		"identifier exactly":    {identifier: []string{"mqtt", "zigbee2mqtt_0x00158D0004AAF230"}, wantErr: `no device has identifier`},
		"identifier pair":       {identifier: []string{"zigbee2mqtt_0x00158d0004aaf230", "mqtt"}, wantErr: `no device has identifier`},
		"ambiguous identifier":  {identifier: []string{"moon", "7"}, wantErr: `2 devices have identifier ["moon" "7"]: d3, d4;`},
		"by ID":                 {id: str("d2"), wantID: "d2"},
		"unknown ID":            {id: str("d9"), wantErr: `no device has ID "d9"`},
		"name_by_user wins":     {name: str("Desk Lamp"), wantID: "d1"},
		"falls back to name":    {name: str("Moon"), wantID: "d3"},
		"ambiguous name":        {name: str("Bulb"), wantErr: `2 devices have name "Bulb": d1, d2;`},
		"unknown name":          {name: str("Sun"), wantErr: `no device has name "Sun"`},
		"name compares exactly": {name: str("moon"), wantErr: `no device has name "moon"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := matchDevice(testDevices, tc.id, tc.name, tc.identifier)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got.ID != tc.wantID {
				t.Errorf("matchDevice = %+v, %v; want %s", got, err, tc.wantID)
			}
		})
	}
}

func TestNewDeviceModelPairs(t *testing.T) {
	m := newDeviceModel(testDevices[0])
	if got := m.Identifiers.String(); got != `[["mqtt","zigbee2mqtt_0x00158d0004aaf230"],["moon","7"]]` {
		t.Errorf("identifiers = %s", got)
	}
	if got := m.Connections.String(); got != `[["mac","dc:a6:32:54:1f:88"]]` {
		t.Errorf("connections = %s", got)
	}
	// A device without them gets empty lists, not null.
	m = newDeviceModel(testDevices[1])
	if m.Identifiers.IsNull() || len(m.Identifiers.Elements()) != 0 || m.Connections.IsNull() || len(m.Connections.Elements()) != 0 {
		t.Errorf("identifiers = %s, connections = %s; want empty lists", m.Identifiers, m.Connections)
	}
}
