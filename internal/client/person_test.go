package client

import (
	"reflect"
	"testing"
)

const personList = `{
	"storage": [{"id":"jane","name":"Jane","user_id":"u2","device_trackers":["device_tracker.phone"],"picture":null}],
	"config": [{"id":"yaml_bob","name":"Bob","user_id":null,"device_trackers":[]}]
}`

func TestPersonsGet(t *testing.T) {
	for _, tc := range []struct {
		id       string
		ok, yaml bool
	}{
		{"jane", true, false},
		{"yaml_bob", false, true},
		{"nobody", false, false},
	} {
		f := &fakeCommander{replies: []fakeReply{{result: personList}}}
		p, ok, yaml, err := NewPersons(f).Get(t.Context(), tc.id)
		if err != nil || ok != tc.ok || yaml != tc.yaml {
			t.Errorf("Get(%q) = %v, %v, %v; want ok %v, yaml %v", tc.id, ok, yaml, err, tc.ok, tc.yaml)
		}
		if tc.ok {
			want := Person{ID: "jane", Name: "Jane", UserID: ptr("u2"), DeviceTrackers: []string{"device_tracker.phone"}}
			if !reflect.DeepEqual(p, want) {
				t.Errorf("Get(%q) = %+v, want %+v", tc.id, p, want)
			}
		}
	}
}

func TestPersonsWrite(t *testing.T) {
	f := &fakeCommander{replies: []fakeReply{
		{result: `{"id":"kid","name":"Kid","user_id":null,"device_trackers":[],"picture":null}`},
		{result: `{"id":"kid","name":"Kiddo","user_id":null,"device_trackers":[],"picture":null}`},
		{},
	}}
	p := NewPersons(f)
	if created, err := p.Create(t.Context(), map[string]any{"name": "Kid"}); err != nil || created.ID != "kid" {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	if _, err := p.Update(t.Context(), "kid", map[string]any{"name": "Kiddo"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(t.Context(), "kid"); err != nil {
		t.Fatal(err)
	}
	want := []fakeCall{
		{"person/create", map[string]any{"name": "Kid"}},
		{"person/update", map[string]any{"person_id": "kid", "name": "Kiddo"}},
		{"person/delete", map[string]any{"person_id": "kid"}},
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %+v, want %+v", f.calls, want)
	}
}

func TestPersonsDeleteYAMLIsNotGone(t *testing.T) {
	f := &fakeCommander{replies: []fakeReply{
		{err: &WSError{Code: "not_found", Message: "Unable to find person_id yaml_bob"}},
		{result: personList},
	}}
	if err := NewPersons(f).Delete(t.Context(), "yaml_bob"); err == nil {
		t.Error("Delete of a person defined in YAML = nil, want the error")
	}
}
