package resources

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// appearingEntity is a WSCommander whose entity is in the registry from the after-th `get` on.
type appearingEntity struct {
	after int
	gets  int
}

func (f *appearingEntity) Command(_ context.Context, typ string, params map[string]any, result any) error {
	if typ != "config/entity_registry/get" {
		return &client.WSError{Code: "unknown_command", Message: typ}
	}
	f.gets++
	if f.gets < f.after {
		return &client.WSError{Code: "not_found", Message: "Entity not found"}
	}
	b, _ := json.Marshal(map[string]any{"entity_id": params["entity_id"], "platform": "moon"})
	return json.Unmarshal(b, result)
}

func fastPolls(t *testing.T) {
	t.Helper()
	interval, maxInterval := pollInterval, maxPollInterval
	pollInterval, maxPollInterval = time.Millisecond, 2*time.Millisecond
	t.Cleanup(func() { pollInterval, maxPollInterval = interval, maxInterval })
}

func TestAwaitEntityWaitsUntilItAppears(t *testing.T) {
	fastPolls(t)
	f := &appearingEntity{after: 4}
	r := &entitySettingsResource{entities: client.Entities(f)}
	entry, err := r.awaitEntity(context.Background(), "sensor.moon_phase", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if f.gets != 4 || entry.Platform != "moon" {
		t.Errorf("gets = %d, entry = %+v", f.gets, entry)
	}
}

func TestAwaitEntityTimesOut(t *testing.T) {
	fastPolls(t)
	r := &entitySettingsResource{entities: client.Entities(&appearingEntity{after: 1 << 30})}
	_, err := r.awaitEntity(context.Background(), "sensor.typo", 20*time.Millisecond)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"sensor.typo", "did not appear", "unique ID", "timeouts.create"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestEntitySettingsChanges(t *testing.T) {
	ctx := context.Background()
	labels := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("managed")})
	user := "user"
	managed := entitySettingsModel{
		EntityID: types.StringValue("sensor.moon_phase"),
		Name:     types.StringValue("Moon"),
		Labels:   labels,
		Hidden:   types.BoolValue(true),
		Disabled: types.BoolValue(false),
	}

	tests := []struct {
		name     string
		to, from entitySettingsModel
		want     map[string]any
	}{
		{
			name: "create writes only the set fields",
			to:   managed,
			want: map[string]any{"name": "Moon", "labels": []string{"managed"}, "hidden_by": &user},
		},
		{
			name: "create clears a flag the user set",
			to:   managed,
			from: entitySettingsModel{Disabled: types.BoolValue(true)},
			want: map[string]any{"name": "Moon", "labels": []string{"managed"}, "hidden_by": &user, "disabled_by": nil},
		},
		{
			name: "destroy resets only the set fields, and no false flag",
			from: managed,
			want: map[string]any{"name": nil, "labels": []string{}, "hidden_by": nil},
		},
		{
			name: "update writes what changed",
			to: func() entitySettingsModel {
				m := managed
				m.Name = types.StringNull()
				m.Icon = types.StringValue("mdi:moon-full")
				return m
			}(),
			from: managed,
			want: map[string]any{"name": nil, "icon": "mdi:moon-full"},
		},
		{
			name: "no changes",
			to:   managed,
			from: managed,
			want: map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := tt.to.changes(ctx, tt.from)
			if diags.HasError() {
				t.Fatal(diags)
			}
			if !maps.EqualFunc(got, tt.want, func(a, b any) bool {
				ja, _ := json.Marshal(a)
				jb, _ := json.Marshal(b)
				return string(ja) == string(jb)
			}) {
				t.Errorf("changes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEntitySettingsRefreshIsFieldGranular(t *testing.T) {
	user, integration, name, icon := "user", "integration", "Moon", "mdi:moon-full"
	entry := client.EntityEntry{
		EntityID: "sensor.moon_phase", Name: &name, Icon: &icon, Labels: []string{"other"},
		HiddenBy: &integration, DisabledBy: &user, Platform: "moon", OriginalName: &name,
	}
	m := entitySettingsModel{
		EntityID: types.StringValue("sensor.moon_phase"),
		Name:     types.StringValue("Old"),
		Hidden:   types.BoolValue(true),
		Disabled: types.BoolValue(false),
	}
	if diags := m.refresh(context.Background(), entry); diags.HasError() {
		t.Fatal(diags)
	}

	if m.Name.ValueString() != "Moon" || m.Platform.ValueString() != "moon" || m.OriginalName.ValueString() != "Moon" {
		t.Errorf("managed and computed attributes not read: %+v", m)
	}
	if !m.Icon.IsNull() || !m.Labels.IsNull() || !m.AreaID.IsNull() {
		t.Errorf("unmanaged attributes were read: %+v", m)
	}
	if m.Hidden.ValueBool() {
		t.Error(`hidden_by "integration" read as hidden = true`)
	}
	if !m.Disabled.ValueBool() {
		t.Error(`disabled_by "user" read as disabled = false`)
	}
	if !m.DeviceID.IsNull() {
		t.Errorf("device_id = %v, want null", m.DeviceID)
	}
}

func TestEntitySettingsWithRegistryFlags(t *testing.T) {
	user, integration := "user", "integration"
	entry := client.EntityEntry{HiddenBy: &integration, DisabledBy: &user}
	to := entitySettingsModel{Hidden: types.BoolValue(false), Disabled: types.BoolValue(false)}

	if !to.needsRegistryFlags(entitySettingsModel{}) {
		t.Error("newly configured false flags do not need the registry")
	}
	if to.needsRegistryFlags(entitySettingsModel{Hidden: types.BoolValue(false), Disabled: types.BoolValue(true)}) {
		t.Error("managed flags need the registry")
	}
	from := to.withRegistryFlags(entitySettingsModel{}, entry)
	fields, diags := to.changes(context.Background(), from)
	if diags.HasError() {
		t.Fatal(diags)
	}
	// hidden_by "integration" stays; disabled_by "user" is cleared.
	if v, ok := fields["disabled_by"]; !ok || v != nil || len(fields) != 1 {
		t.Errorf("changes = %v, want only disabled_by: null", fields)
	}
}

// fakeEntityRegistry is a WSCommander over the entries, keyed by entity ID. It records the
// commands it runs and the params of the last update.
type fakeEntityRegistry struct {
	entries      map[string]client.EntityEntry
	calls        []string
	updateParams map[string]any
}

func (f *fakeEntityRegistry) Command(_ context.Context, typ string, params map[string]any, result any) error {
	f.calls = append(f.calls, strings.TrimPrefix(typ, "config/entity_registry/"))
	var reply any
	switch typ {
	case "config/entity_registry/get":
		e, ok := f.entries[params["entity_id"].(string)]
		if !ok {
			return &client.WSError{Code: "not_found", Message: "Entity not found"}
		}
		reply = e
	case "config/entity_registry/list":
		reply = slices.Collect(maps.Values(f.entries))
	case "config/entity_registry/update":
		f.updateParams = params
		e, ok := f.entries[params["entity_id"].(string)]
		if !ok {
			return &client.WSError{Code: "not_found", Message: "Entity not found"}
		}
		if n, ok := params["new_entity_id"].(string); ok {
			if _, taken := f.entries[n]; taken {
				return &client.WSError{Code: "invalid_info", Message: "Entity with this ID is already registered"}
			}
			delete(f.entries, e.EntityID)
			e.EntityID = n
			f.entries[n] = e
		}
		reply = map[string]any{"entity_entry": e}
	default:
		return &client.WSError{Code: "unknown_command", Message: typ}
	}
	b, _ := json.Marshal(reply)
	return json.Unmarshal(b, result)
}

// entitySettingsState is a minimal state of the resource for id and entityID.
func entitySettingsState(id, entityID string) entitySettingsModel {
	return entitySettingsModel{
		ID:           types.StringValue(id),
		EntityID:     types.StringValue(entityID),
		Labels:       types.SetNull(types.StringType),
		Platform:     types.StringValue("moon"),
		Timeouts:     timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{"create": types.StringType})},
		Name:         types.StringNull(),
		Icon:         types.StringNull(),
		AreaID:       types.StringNull(),
		Hidden:       types.BoolNull(),
		Disabled:     types.BoolNull(),
		DeviceID:     types.StringNull(),
		OriginalName: types.StringNull(),
	}
}

// readEntitySettings runs Read on state and returns the new state; ok is false if Read removed
// the resource.
func readEntitySettings(t *testing.T, f *fakeEntityRegistry, state entitySettingsModel) (got entitySettingsModel, ok bool) {
	t.Helper()
	r := &entitySettingsResource{entities: client.Entities(f)}
	s := stateOf(t, r, state)
	resp := resource.ReadResponse{State: s}
	r.Read(t.Context(), resource.ReadRequest{State: s}, &resp)
	checkDiags(t, resp.Diagnostics, "")
	if resp.State.Raw.IsNull() {
		return got, false
	}
	if diags := resp.State.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	return got, true
}

func TestEntitySettingsReadFindsByRegistryID(t *testing.T) {
	registry := func() *fakeEntityRegistry {
		return &fakeEntityRegistry{entries: map[string]client.EntityEntry{
			"sensor.renamed":    {ID: "abc", EntityID: "sensor.renamed", Platform: "moon"},
			"sensor.moon_phase": {ID: "def", EntityID: "sensor.moon_phase", Platform: "moon"},
		}}
	}
	tests := []struct {
		name, id, entityID string
		wantOK             bool
		wantID, wantEntity string
		wantCalls          []string
	}{
		{"unchanged", "abc", "sensor.renamed", true, "abc", "sensor.renamed", []string{"get"}},
		{"renamed: get misses", "abc", "sensor.old", true, "abc", "sensor.renamed", []string{"get", "list"}},
		{"renamed: entity ID now another entity's", "abc", "sensor.moon_phase", true, "abc", "sensor.renamed", []string{"get", "list"}},
		{"gone", "xyz", "sensor.old", false, "", "", []string{"get", "list"}},
		{"state without id gets it", "", "sensor.moon_phase", true, "def", "sensor.moon_phase", []string{"get"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := registry()
			state := entitySettingsState(tt.id, tt.entityID)
			if tt.id == "" {
				state.ID = types.StringNull()
			}
			got, ok := readEntitySettings(t, f, state)
			if ok != tt.wantOK || (ok && (got.ID.ValueString() != tt.wantID || got.EntityID.ValueString() != tt.wantEntity)) {
				t.Errorf("Read = %v, %v / %v; want ok %v, id %q, entity_id %q", ok, got.ID, got.EntityID, tt.wantOK, tt.wantID, tt.wantEntity)
			}
			if !slices.Equal(f.calls, tt.wantCalls) {
				t.Errorf("calls = %q, want %q", f.calls, tt.wantCalls)
			}
		})
	}
}

// updateEntitySettings runs Update from state to plan and returns the new state and the
// diagnostics. Like the framework, it starts the new state from the plan.
func updateEntitySettings(t *testing.T, f *fakeEntityRegistry, state, plan entitySettingsModel) (entitySettingsModel, diag.Diagnostics) {
	t.Helper()
	r := &entitySettingsResource{entities: client.Entities(f)}
	planned := stateOf(t, r, plan)
	resp := resource.UpdateResponse{State: planned}
	r.Update(t.Context(), resource.UpdateRequest{State: stateOf(t, r, state), Plan: tfsdk.Plan(planned)}, &resp)
	var got entitySettingsModel
	if diags := resp.State.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	return got, resp.Diagnostics
}

func TestEntitySettingsUpdateRenames(t *testing.T) {
	f := &fakeEntityRegistry{entries: map[string]client.EntityEntry{
		"sensor.moon_phase": {ID: "abc", EntityID: "sensor.moon_phase", Platform: "moon"},
	}}
	state := entitySettingsState("abc", "sensor.moon_phase")
	state.Name = types.StringValue("Moon")
	plan := state
	plan.EntityID = types.StringValue("sensor.moon")
	plan.Name = types.StringValue("Full Moon")

	got, diags := updateEntitySettings(t, f, state, plan)
	checkDiags(t, diags, "")
	if got.EntityID.ValueString() != "sensor.moon" || got.ID.ValueString() != "abc" {
		t.Errorf("state = %v / %v, want abc / sensor.moon", got.ID, got.EntityID)
	}
	want := map[string]any{"entity_id": "sensor.moon_phase", "new_entity_id": "sensor.moon", "name": "Full Moon"}
	if !maps.EqualFunc(f.updateParams, want, func(a, b any) bool {
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		return string(ja) == string(jb)
	}) {
		t.Errorf("update params = %v, want %v in one update", f.updateParams, want)
	}
	if e, ok := f.entries["sensor.moon"]; !ok || e.ID != "abc" {
		t.Errorf("entries = %v, want abc renamed to sensor.moon", f.entries)
	}
}

func TestEntitySettingsUpdateRenameToTakenEntityID(t *testing.T) {
	f := &fakeEntityRegistry{entries: map[string]client.EntityEntry{
		"sensor.moon_phase": {ID: "abc", EntityID: "sensor.moon_phase"},
		"sensor.moon":       {ID: "def", EntityID: "sensor.moon"},
	}}
	state := entitySettingsState("abc", "sensor.moon_phase")
	plan := state
	plan.EntityID = types.StringValue("sensor.moon")

	got, diags := updateEntitySettings(t, f, state, plan)
	checkDiags(t, diags, "already registered")
	checkDiags(t, diags, "tofu apply -replace=")
	if got.EntityID.ValueString() != "sensor.moon_phase" {
		t.Errorf("entity_id in state = %v, want the prior sensor.moon_phase", got.EntityID)
	}
}
