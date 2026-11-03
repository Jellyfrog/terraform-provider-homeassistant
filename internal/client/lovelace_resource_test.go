package client

import (
	"reflect"
	"testing"
)

func TestLovelaceResources(t *testing.T) {
	f := &fakeCommander{replies: []fakeReply{
		{result: `[{"id":"a1","type":"module","url":"/hacsfiles/card.js"}]`},
		{result: `{"id":"b2","type":"css","url":"/local/theme.css"}`},
		{result: `{"id":"b2","type":"css","url":"/local/theme2.css"}`},
	}}
	r := LovelaceResources(f)

	got, ok, err := r.Get(t.Context(), "a1")
	if err != nil || !ok || got != (LovelaceResource{ID: "a1", Type: "module", URL: "/hacsfiles/card.js"}) {
		t.Fatalf("Get = %+v, %v, %v", got, ok, err)
	}
	created, err := r.Create(t.Context(), map[string]any{"res_type": "css", "url": "/local/theme.css"})
	if err != nil || created.ID != "b2" {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	if _, err := r.Update(t.Context(), "b2", map[string]any{"res_type": "css", "url": "/local/theme2.css"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	want := []fakeCall{
		{"lovelace/resources", nil},
		{"lovelace/resources/create", map[string]any{"res_type": "css", "url": "/local/theme.css"}},
		{"lovelace/resources/update", map[string]any{"resource_id": "b2", "res_type": "css", "url": "/local/theme2.css"}},
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %+v, want %+v", f.calls, want)
	}
}
