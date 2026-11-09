package resources

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// fakeLovelaceResources is a WSCommander serving Lovelace resources. In YAML mode it lists yaml (which
// have no ID) and knows no create, update or delete command, like Home Assistant.
type fakeLovelaceResources struct {
	mode    string
	stored  []client.LovelaceResource
	yaml    []client.LovelaceResource
	deleted []string
}

func (f *fakeLovelaceResources) Command(_ context.Context, typ string, params map[string]any, result any) error {
	reply := func(v any) error {
		b, _ := json.Marshal(v)
		return json.Unmarshal(b, result)
	}
	switch {
	case typ == "lovelace/info":
		return reply(map[string]string{"resource_mode": f.mode})
	case typ == "lovelace/resources" && f.mode == "yaml":
		return reply(f.yaml)
	case typ == "lovelace/resources":
		return reply(f.stored)
	case typ == "lovelace/resources/delete" && f.mode == "storage":
		id, _ := params["resource_id"].(string)
		f.deleted = append(f.deleted, id)
		f.stored = slices.DeleteFunc(f.stored, func(r client.LovelaceResource) bool { return r.ID == id })
		return nil
	}
	return &client.WSError{Code: "unknown_command", Message: typ}
}

func lovelaceState(t *testing.T, id string) tfsdk.State {
	t.Helper()
	return stateOf(t, &lovelaceResourceResource{}, lovelaceResourceModel{
		ID: types.StringValue(id), URL: types.StringValue("/local/card.js"), Type: types.StringValue("module"),
	})
}

func readLovelace(t *testing.T, f *fakeLovelaceResources, id string) resource.ReadResponse {
	t.Helper()
	state := lovelaceState(t, id)
	resp := resource.ReadResponse{State: state}
	newLovelaceResourceResource(f).Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	return resp
}

func TestLovelaceResourceReadGoneRemovesResource(t *testing.T) {
	resp := readLovelace(t, &fakeLovelaceResources{mode: "storage"}, "a1")
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("read of a deleted resource: state null = %v, %v; want removed, no error", resp.State.Raw.IsNull(), resp.Diagnostics)
	}
}

func TestLovelaceResourceReadInYAMLModeFails(t *testing.T) {
	f := &fakeLovelaceResources{mode: "yaml", yaml: []client.LovelaceResource{{Type: "module", URL: "/local/card.js"}}}
	resp := readLovelace(t, f, "a1")
	checkDiags(t, resp.Diagnostics, "resource_mode")
	if resp.State.Raw.IsNull() {
		t.Error("read in YAML mode removed the resource from state")
	}
}

func TestLovelaceResourceDeleteInYAMLModeFails(t *testing.T) {
	f := &fakeLovelaceResources{mode: "yaml"}
	var resp resource.DeleteResponse
	newLovelaceResourceResource(f).Delete(t.Context(), resource.DeleteRequest{State: lovelaceState(t, "a1")}, &resp)
	checkDiags(t, resp.Diagnostics, "lovelace/resources/delete")
}

func TestLovelaceResourceDelete(t *testing.T) {
	f := &fakeLovelaceResources{mode: "storage", stored: []client.LovelaceResource{{ID: "a1", Type: "module", URL: "/local/card.js"}}}
	var resp resource.DeleteResponse
	newLovelaceResourceResource(f).Delete(t.Context(), resource.DeleteRequest{State: lovelaceState(t, "a1")}, &resp)
	if resp.Diagnostics.HasError() || !slices.Equal(f.deleted, []string{"a1"}) {
		t.Errorf("delete: deleted %v, %v; want [a1], no error", f.deleted, resp.Diagnostics)
	}
}
