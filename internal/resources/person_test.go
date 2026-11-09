package resources

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// fakePersons is a WSCommander serving persons like Home Assistant: an ID is the slug of the
// name it was created with, and an update without device_trackers clears them.
type fakePersons struct {
	storage, yaml []client.Person
	updates       []map[string]any
}

func (f *fakePersons) Command(_ context.Context, typ string, params map[string]any, result any) error {
	reply := func(v any) error { return jsonReply(v, result) }
	// apply sets the fields in params on p, through JSON like Home Assistant gets them.
	apply := func(p *client.Person, params map[string]any) {
		b, _ := json.Marshal(params)
		_ = json.Unmarshal(b, p)
	}
	switch typ {
	case "person/list":
		return reply(map[string]any{"storage": f.storage, "config": f.yaml})
	case "person/create":
		p := client.Person{ID: client.Slugify(params["name"].(string)), DeviceTrackers: []string{}}
		apply(&p, params)
		f.storage = append(f.storage, p)
		return reply(p)
	case "person/update":
		f.updates = append(f.updates, params)
		for i := range f.storage {
			if f.storage[i].ID == params["person_id"] {
				p := &f.storage[i]
				p.DeviceTrackers = []string{}
				apply(p, params)
				p.ID = params["person_id"].(string)
				return reply(p)
			}
		}
		return &client.WSError{Code: "not_found", Message: "not found"}
	case "person/delete":
		f.storage = slices.DeleteFunc(f.storage, func(p client.Person) bool { return p.ID == params["person_id"] })
		return nil
	}
	return &client.WSError{Code: "unknown_command", Message: typ}
}

func personRes(f *fakePersons) *personResource {
	return &personResource{persons: client.NewPersons(f)}
}

// emptyPerson is the model of an empty state. A zero types.Set has no element type, which the
// framework rejects.
func emptyPerson() personModel {
	return personModel{DeviceTrackers: types.SetNull(types.StringType)}
}

func janePerson(id string) personModel {
	return personModel{
		ID: types.StringValue(id), Name: types.StringValue("Jane"), UserID: types.StringValue("u1"),
		DeviceTrackers: types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("device_tracker.tablet"), types.StringValue("device_tracker.phone"),
		}),
		Picture: types.StringNull(),
	}
}

func TestPersonCreateWithIDSendsAllFieldsOnRename(t *testing.T) {
	f := &fakePersons{}
	r := personRes(f)
	resp := resource.CreateResponse{State: stateOf(t, r, emptyPerson())}
	r.Create(t.Context(), resource.CreateRequest{Plan: planOf(t, r, janePerson("jane_doe"))}, &resp)
	checkDiags(t, resp.Diagnostics, "")

	want := client.Person{
		ID: "jane_doe", Name: "Jane", UserID: ptr("u1"),
		DeviceTrackers: []string{"device_tracker.phone", "device_tracker.tablet"},
	}
	if len(f.storage) != 1 || !reflect.DeepEqual(f.storage[0], want) {
		t.Errorf("in Home Assistant: %+v, want %+v", f.storage, want)
	}
}

func TestPersonReadAndUpdate(t *testing.T) {
	f := &fakePersons{storage: []client.Person{{
		ID: "jane", Name: "Jane", UserID: ptr("u1"),
		DeviceTrackers: []string{"device_tracker.tablet", "device_tracker.phone"},
	}}}
	r := personRes(f)
	state := stateOf(t, r, janePerson("jane"))
	rresp := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &rresp)
	checkDiags(t, rresp.Diagnostics, "")
	var got personModel
	rresp.State.Get(t.Context(), &got)
	if !reflect.DeepEqual(got, janePerson("jane")) {
		t.Errorf("read = %+v", got)
	}

	// Unsetting user_id sends null, which clears it; trackers are always sent, sorted.
	plan := janePerson("jane")
	plan.UserID, plan.Picture = types.StringNull(), types.StringValue("/local/jane.png")
	uresp := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{Plan: planOf(t, r, plan), State: state}, &uresp)
	checkDiags(t, uresp.Diagnostics, "")
	sent := f.updates[0]
	if sent["user_id"] != (*string)(nil) || !slices.Equal(sent["device_trackers"].([]string),
		[]string{"device_tracker.phone", "device_tracker.tablet"}) {
		t.Errorf("update sent %v", sent)
	}
	if p := f.storage[0]; p.UserID != nil || p.Picture == nil || *p.Picture != "/local/jane.png" {
		t.Errorf("in Home Assistant: %+v", p)
	}
}

func TestPersonReadGoneAndYAML(t *testing.T) {
	f := &fakePersons{yaml: []client.Person{{ID: "bob", Name: "Bob"}}}
	r := personRes(f)
	for id, want := range map[string]string{"jane": "", "bob": "YAML"} {
		state := stateOf(t, r, janePerson(id))
		resp := resource.ReadResponse{State: state}
		r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
		checkDiags(t, resp.Diagnostics, want)
		if removed := resp.State.Raw.IsNull(); removed != (want == "") {
			t.Errorf("read %q: removed = %v", id, removed)
		}
	}
}

func TestPersonImport(t *testing.T) {
	r := personRes(&fakePersons{})
	resp := resource.ImportStateResponse{State: stateOf(t, r, emptyPerson())}
	r.ImportState(t.Context(), resource.ImportStateRequest{ID: "jane"}, &resp)
	checkDiags(t, resp.Diagnostics, "")
	var got types.String
	resp.State.GetAttribute(t.Context(), path.Root("id"), &got)
	if got.ValueString() != "jane" {
		t.Errorf("import: id %q, want jane", got.ValueString())
	}
}
