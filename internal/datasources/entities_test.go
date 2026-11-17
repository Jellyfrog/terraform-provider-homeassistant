package datasources

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

var testStates = []client.EntityState{
	{EntityID: "sensor.kitchen_temp", Attributes: json.RawMessage(`{"friendly_name":"Kitchen Temp","device_class":"temperature"}`)},
	{EntityID: "light.kitchen", Attributes: json.RawMessage(`{"friendly_name":"Kitchen Ceiling"}`)},
	{EntityID: "light.hall", Attributes: json.RawMessage(`{"friendly_name":"Hall"}`)},
	{EntityID: "sensor.hall_temp", Attributes: json.RawMessage(`{"friendly_name":"Hall Temp","device_class":"temperature"}`)},
	{EntityID: "lightning.strike", Attributes: json.RawMessage(`{}`)},
	{EntityID: "sensor.nameless", Attributes: json.RawMessage(`{"device_class":5}`)},
}

func TestEntityFilter(t *testing.T) {
	for name, tc := range map[string]struct {
		filter entityFilter
		want   []string
	}{
		"none, sorted": {entityFilter{}, []string{
			"light.hall", "light.kitchen", "lightning.strike", "sensor.hall_temp", "sensor.kitchen_temp", "sensor.nameless",
		}},
		"domain is a whole prefix": {entityFilter{domain: "light"}, []string{"light.hall", "light.kitchen"}},
		"area": {
			entityFilter{area: map[string]bool{"light.kitchen": true, "sensor.kitchen_temp": true}},
			[]string{"light.kitchen", "sensor.kitchen_temp"},
		},
		"empty area":   {entityFilter{area: map[string]bool{}}, []string{}},
		"label":        {entityFilter{label: map[string]bool{"light.hall": true}}, []string{"light.hall"}},
		"device":       {entityFilter{device: map[string]bool{"sensor.hall_temp": true}}, []string{"sensor.hall_temp"}},
		"empty device": {entityFilter{device: map[string]bool{}}, []string{}},
		"device class": {
			entityFilter{deviceClass: "temperature"},
			[]string{"sensor.hall_temp", "sensor.kitchen_temp"},
		},
		"name pattern":            {entityFilter{name: globRegexp("*Temp")}, []string{"sensor.hall_temp", "sensor.kitchen_temp"}},
		"name pattern needs name": {entityFilter{name: globRegexp("*")}, []string{"light.hall", "light.kitchen", "sensor.hall_temp", "sensor.kitchen_temp"}},
		"not hidden": {
			entityFilter{hidden: new(false), hiddenIDs: map[string]bool{"light.hall": true, "sensor.nameless": true}},
			[]string{"light.kitchen", "lightning.strike", "sensor.hall_temp", "sensor.kitchen_temp"},
		},
		"hidden": {
			entityFilter{hidden: new(true), hiddenIDs: map[string]bool{"light.hall": true, "sensor.nameless": true}},
			[]string{"light.hall", "sensor.nameless"},
		},
		"hidden unset": {
			entityFilter{domain: "light", hiddenIDs: map[string]bool{"light.hall": true}},
			[]string{"light.hall", "light.kitchen"},
		},
		"all combined": {
			entityFilter{
				domain:      "sensor",
				deviceClass: "temperature",
				name:        globRegexp("K*"),
				area:        map[string]bool{"sensor.kitchen_temp": true, "sensor.hall_temp": true, "light.kitchen": true},
				label:       map[string]bool{"sensor.kitchen_temp": true, "sensor.hall_temp": true},
				device:      map[string]bool{"sensor.kitchen_temp": true, "light.kitchen": true},
			},
			[]string{"sensor.kitchen_temp"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := pluck(tc.filter.apply(testStates), func(s client.EntityState) string { return s.EntityID })
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGlobRegexp(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"Kitchen*", "Kitchen Ceiling", true},
		{"Kitchen*", "kitchen ceiling", false},
		{"*Temp", "Hall Temp", true},
		{"Hall ?emp", "Hall Temp", true},
		{"Hall ?emp", "Hall Teemp", false},
		{"a.b", "axb", false},
		{"(x)+[y]", "(x)+[y]", true},
		{"*", "line\nbreak", true},
		{"Hall", "Hall Temp", false},
	} {
		if got := globRegexp(tc.pattern).MatchString(tc.name); got != tc.want {
			t.Errorf("%q matches %q = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestJinjaString(t *testing.T) {
	for in, want := range map[string]string{
		"living_room":             `"living_room"`,
		"Bob's room":              `"Bob's room"`,
		`say "hi"`:                `"say \u0022hi\u0022"`,
		`back\slash`:              `"back\u005cslash"`,
		"}}{% x %}":               `"}}{% x %}"`,
		"new\nline":               `"new\u000aline"`,
		"K\u00fcche":              `"K\u00fcche"`,
		"\U0001f6cb\ufe0f lounge": `"\U0001f6cb\ufe0f lounge"`,
	} {
		if got := jinjaString(in); got != want {
			t.Errorf("jinjaString(%q) = %s, want %s", in, got, want)
		}
	}
}

type fakeEntitiesReader struct {
	templates []string
	result    string
}

func (f *fakeEntitiesReader) States(context.Context) ([]client.EntityState, error) {
	return testStates, nil
}

func (f *fakeEntitiesReader) RenderTemplate(_ context.Context, template string) (string, error) {
	f.templates = append(f.templates, template)
	return f.result, nil
}

func TestEntityIDsOfEscapesTheValue(t *testing.T) {
	for _, function := range []string{"area_entities", "label_entities", "device_entities"} {
		t.Run(function, func(t *testing.T) {
			fake := &fakeEntitiesReader{result: `["light.kitchen","sensor.kitchen_temp"]`}
			d := entitiesDataSource{client: fake}
			got, err := d.entityIDsOf(t.Context(), function, `x") + states("y`)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{`{{ ` + function + `("x\u0022) + states(\u0022y") | tojson }}`}
			if !reflect.DeepEqual(fake.templates, want) {
				t.Errorf("templates = %q, want %q", fake.templates, want)
			}
			if !reflect.DeepEqual(got, map[string]bool{"light.kitchen": true, "sensor.kitchen_temp": true}) {
				t.Errorf("ids = %v", got)
			}
		})
	}
}

func TestHiddenEntityIDs(t *testing.T) {
	fake := &fakeEntitiesReader{result: `["light.hall"]`}
	d := entitiesDataSource{client: fake}
	got, err := d.hiddenEntityIDs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`{{ states | map(attribute="entity_id") | select("is_hidden_entity") | list | tojson }}`}; !reflect.DeepEqual(fake.templates, want) {
		t.Errorf("templates = %q, want %q", fake.templates, want)
	}
	if !reflect.DeepEqual(got, map[string]bool{"light.hall": true}) {
		t.Errorf("ids = %v", got)
	}
}

func TestEntitiesValue(t *testing.T) {
	v, err := entitiesValue([]client.EntityState{
		{EntityID: "sensor.t", State: "21.5", LastChanged: "2026-10-04T10:00:00+00:00", Attributes: json.RawMessage(`{"friendly_name":"T","precision":1}`)},
		{EntityID: "sun.sun", State: "above_horizon"},
	}, map[string]bool{"sun.sun": true})
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := v.UnderlyingValue().(basetypes.ObjectValue)
	if !ok {
		t.Fatalf("entities = %T, want object", v.UnderlyingValue())
	}
	sensor := obj.Attributes()["sensor.t"].(basetypes.ObjectValue).Attributes()
	if sensor["state"] != types.StringValue("21.5") || sensor["friendly_name"] != types.StringValue("T") ||
		sensor["last_changed"] != types.StringValue("2026-10-04T10:00:00+00:00") {
		t.Errorf("sensor.t = %v", sensor)
	}
	if _, ok := sensor["attributes"].(basetypes.ObjectValue).Attributes()["precision"].(basetypes.NumberValue); !ok {
		t.Errorf("precision is not a number: %v", sensor["attributes"])
	}
	if sensor["hidden"] != types.BoolValue(false) {
		t.Errorf("hidden of sensor.t = %v, want false", sensor["hidden"])
	}
	sun := obj.Attributes()["sun.sun"].(basetypes.ObjectValue).Attributes()
	if sun["hidden"] != types.BoolValue(true) {
		t.Errorf("hidden of sun.sun = %v, want true", sun["hidden"])
	}
	if !sun["friendly_name"].IsNull() {
		t.Errorf("friendly_name of sun.sun = %v, want null", sun["friendly_name"])
	}
}
