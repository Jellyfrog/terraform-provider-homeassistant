package resources

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
	"github.com/toelke/terraform-provider-homeassistant/internal/dyntype"
)

func TestURLPathValidator(t *testing.T) {
	cases := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"hyphen":    {types.StringValue("my-home"), false},
		"no hyphen": {types.StringValue("home"), true},
		"null":      {types.StringNull(), false},
		"unknown":   {types.StringUnknown(), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			urlPathValidator.ValidateString(t.Context(), validator.StringRequest{ConfigValue: tc.value}, &resp)
			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Errorf("error = %v, want %v: %v", got, tc.wantErr, resp.Diagnostics)
			}
			// The error shows what a valid value looks like.
			if resp.Diagnostics.HasError() && !strings.Contains(resp.Diagnostics[0].Detail(), "e.g. \"my-home\"") {
				t.Errorf("detail = %q, want an example", resp.Diagnostics[0].Detail())
			}
		})
	}
}

func TestDashboardMetaChanged(t *testing.T) {
	base := dashboardModel{
		Title:         types.StringValue("Home"),
		Icon:          types.StringNull(),
		ShowInSidebar: types.BoolValue(true),
		RequireAdmin:  types.BoolValue(false),
	}
	if base.metaChanged(base) {
		t.Error("identical metadata reported as changed")
	}
	// The config and IDs are not metadata.
	other := base
	other.DashboardID = types.StringValue("my_home")
	if base.metaChanged(other) {
		t.Error("dashboard_id reported as a metadata change")
	}
	for name, mutate := range map[string]func(*dashboardModel){
		"title":           func(m *dashboardModel) { m.Title = types.StringValue("Other") },
		"icon":            func(m *dashboardModel) { m.Icon = types.StringValue("mdi:home") },
		"show_in_sidebar": func(m *dashboardModel) { m.ShowInSidebar = types.BoolValue(false) },
		"require_admin":   func(m *dashboardModel) { m.RequireAdmin = types.BoolValue(true) },
	} {
		changed := base
		mutate(&changed)
		if !changed.metaChanged(base) {
			t.Errorf("%s change not detected", name)
		}
	}
}

// A dashboard state without a baseline (from v0.1.0, or after import) compares the config read
// back by semantic equality: key order and number format don't matter, a changed card does.
func TestDashboardConfigWithoutBaselineIsSemanticEquality(t *testing.T) {
	prior, err := dyntype.FromJSON([]byte(`{"views":[{"title":"Home","cards":[{"type":"tile","entity":"sun.sun","columns":6}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		read  string
		equal bool
	}{
		"same config":      {`{"views":[{"cards":[{"columns":6.0,"entity":"sun.sun","type":"tile"}],"title":"Home"}]}`, true},
		"edited in the UI": {`{"views":[{"title":"Home","cards":[{"type":"tile","entity":"sun.sun","columns":12}]}]}`, false},
		"view added in HA": {`{"views":[{"title":"Home","cards":[{"type":"tile","entity":"sun.sun","columns":6}]},{"title":"New"}]}`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := refreshedConfig(prior, json.RawMessage(tc.read), nil)
			if err != nil {
				t.Fatal(err)
			}
			if eq, diags := prior.DynamicSemanticEquals(t.Context(), got); diags.HasError() || eq != tc.equal {
				t.Errorf("semantically equal = %v (%v), want %v", eq, diags, tc.equal)
			}
		})
	}
}

// fakeLovelace is a WSCommander serving one storage-mode dashboard, `my-home`, once created.
type fakeLovelace struct {
	exists bool
	// saveErr, if set, is returned by every config save.
	saveErr error
	// cancel, if set, is called when a config save arrives, like Ctrl-C during the save.
	cancel context.CancelFunc
	calls  []string
}

func (f *fakeLovelace) Command(ctx context.Context, typ string, _ map[string]any, result any) error {
	f.calls = append(f.calls, typ)
	// Like the WebSocket client, a cancelled context fails the command.
	if err := ctx.Err(); err != nil {
		return err
	}
	reply := func(v any) error { return jsonReply(v, result) }
	dash := map[string]any{"id": "my_home", "url_path": "my-home", "mode": "storage", "title": "Home", "show_in_sidebar": true}
	switch typ {
	case "lovelace/dashboards/create":
		f.exists = true
		return reply(dash)
	case "lovelace/dashboards/list":
		if !f.exists {
			return reply([]any{})
		}
		return reply([]any{dash})
	case "lovelace/dashboards/delete":
		f.exists = false
		return nil
	case "lovelace/config/save":
		if f.cancel != nil {
			f.cancel()
			return context.Canceled
		}
		return f.saveErr
	case "lovelace/config":
		return &client.WSError{Code: "config_not_found", Message: "No config found."}
	}
	return &client.WSError{Code: "unknown_command", Message: typ}
}

// planOf returns a plan of resource r holding the model m.
func planOf(t *testing.T, r resource.Resource, m any) tfsdk.Plan {
	t.Helper()
	s := stateOf(t, r, m)
	return tfsdk.Plan(s)
}

// jsonReply decodes v into result through JSON, as a fake WSCommander replies. A nil result
// takes no reply.
func jsonReply(v, result any) error {
	if result == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, result)
}

// stateOf returns m as state of r.
func stateOf(t *testing.T, r resource.Resource, m any) tfsdk.State {
	t.Helper()
	var resp resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &resp)
	state := tfsdk.State{Schema: resp.Schema, Raw: tftypes.NewValue(resp.Schema.Type().TerraformType(t.Context()), nil)}
	if diags := state.Set(t.Context(), m); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func dashboardPlan(t *testing.T) dashboardModel {
	t.Helper()
	return dashboardModel{
		ID:            types.StringUnknown(),
		URLPath:       types.StringValue("my-home"),
		Title:         types.StringValue("Home"),
		Icon:          types.StringNull(),
		ShowInSidebar: types.BoolValue(true),
		RequireAdmin:  types.BoolValue(false),
		Config:        mustDyn(t, `{"views":[{"title":"Home"}]}`),
		DashboardID:   types.StringUnknown(),
	}
}

// createDashboard runs Create with ctx against f and returns its diagnostics.
func createDashboard(t *testing.T, ctx context.Context, f *fakeLovelace) diag.Diagnostics {
	t.Helper()
	r := &dashboardResource{dashboards: client.NewDashboards(f)}
	planned := stateOf(t, r, dashboardPlan(t))
	resp := resource.CreateResponse{State: tfsdk.State{Schema: planned.Schema, Raw: tftypes.NewValue(planned.Raw.Type(), nil)}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan(planned)}, &resp)
	if !resp.State.Raw.IsNull() {
		t.Error("a failed create set state")
	}
	return resp.Diagnostics
}

// A failed config save deletes the dashboard again, so the next apply can create it.
func TestDashboardCreateRollsBackWhenSaveFails(t *testing.T) {
	f := &fakeLovelace{saveErr: &client.WSError{Code: "invalid_format", Message: "bad config"}}
	diags := createDashboard(t, t.Context(), f)

	checkDiags(t, diags, "bad config")
	if want := []string{"lovelace/dashboards/create", "lovelace/config/save", "lovelace/dashboards/delete"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
	if f.exists {
		t.Error("the dashboard was left behind")
	}
}

// Ctrl-C cancels the context while the config is saved: the dashboard is still deleted.
func TestDashboardCreateRollsBackWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := &fakeLovelace{cancel: cancel}
	diags := createDashboard(t, ctx, f)

	if !diags.HasError() {
		t.Error("no error")
	}
	if want := []string{"lovelace/dashboards/create", "lovelace/config/save", "lovelace/dashboards/delete"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
	if f.exists {
		t.Error("the dashboard was left behind")
	}
}

// A dashboard that never had a config saved reads with a null config, so the next apply saves
// the configured one.
func TestDashboardReadWithoutConfigLeavesItNull(t *testing.T) {
	f := &fakeLovelace{exists: true}
	r := &dashboardResource{dashboards: client.NewDashboards(f)}
	prior := dashboardPlan(t)
	prior.ID, prior.DashboardID = types.StringValue("my-home"), types.StringValue("my_home")
	state := stateOf(t, r, prior)
	resp := resource.ReadResponse{State: state}

	r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got dashboardModel
	if diags := resp.State.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	if !got.Config.IsNull() {
		t.Errorf("config = %v, want null", got.Config)
	}
	if got.Title.ValueString() != "Home" || got.DashboardID.ValueString() != "my_home" {
		t.Errorf("state = %+v", got)
	}
}
