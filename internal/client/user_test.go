package client

import (
	"reflect"
	"testing"
)

func TestUsers(t *testing.T) {
	f := &fakeCommander{replies: []fakeReply{
		{result: `[{"id":"u1","username":null,"name":"Owner","is_owner":true,"is_active":true,"local_only":false,
			"system_generated":false,"group_ids":["system-admin"],"credentials":[]},
			{"id":"u2","username":"jane","name":"Jane","is_owner":false,"is_active":false,"local_only":true,
			"system_generated":false,"group_ids":["system-users"],"credentials":[{"type":"homeassistant"}]}]`},
		{result: `{"user":{"id":"u3","username":null,"name":"Kid","is_active":true,"group_ids":["system-read-only"]}}`},
		{result: `{"user":{"id":"u3","username":null,"name":"Kid","is_active":false,"group_ids":["system-read-only"]}}`},
		{},
		{},
		{},
		{},
	}}
	u := NewUsers(f)

	got, ok, err := u.Get(t.Context(), "u2")
	want := User{ID: "u2", Username: ptr("jane"), Name: "Jane", LocalOnly: true, GroupIDs: []string{GroupUser}}
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("Get = %+v, %v, %v; want %+v", got, ok, err, want)
	}
	created, err := u.Create(t.Context(), map[string]any{"name": "Kid", "group_ids": []string{GroupReadOnly}})
	if err != nil || created.ID != "u3" || created.Username != nil {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	updated, err := u.Update(t.Context(), "u3", map[string]any{"is_active": false})
	if err != nil || updated.IsActive {
		t.Fatalf("Update = %+v, %v", updated, err)
	}
	for _, call := range []func() error{
		func() error { return u.CreateCredentials(t.Context(), "u3", "kid", "secret") },
		func() error { return u.ChangeUsername(t.Context(), "u3", "kiddo") },
		func() error { return u.ChangePassword(t.Context(), "u3", "secret2") },
		func() error { return u.DeleteCredentials(t.Context(), "kiddo") },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}

	wantCalls := []fakeCall{
		{"config/auth/list", nil},
		{"config/auth/create", map[string]any{"name": "Kid", "group_ids": []string{GroupReadOnly}}},
		{"config/auth/update", map[string]any{"user_id": "u3", "is_active": false}},
		{"config/auth_provider/homeassistant/create", map[string]any{"user_id": "u3", "username": "kid", "password": "secret"}},
		{"config/auth_provider/homeassistant/admin_change_username", map[string]any{"user_id": "u3", "username": "kiddo"}},
		{"config/auth_provider/homeassistant/admin_change_password", map[string]any{"user_id": "u3", "password": "secret2"}},
		{"config/auth_provider/homeassistant/delete", map[string]any{"username": "kiddo"}},
	}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("calls = %+v, want %+v", f.calls, wantCalls)
	}
}

func TestUsersDeleteGone(t *testing.T) {
	f := &fakeCommander{replies: []fakeReply{
		{err: &WSError{Code: "not_found", Message: "User not found"}},
		{result: `[]`},
	}}
	if err := NewUsers(f).Delete(t.Context(), "u9"); err != nil {
		t.Errorf("Delete of a missing user = %v, want nil", err)
	}
}

func TestUsersCreateReplyWithoutUser(t *testing.T) {
	f := &fakeCommander{replies: []fakeReply{{result: `{"other":1}`}}}
	if _, err := NewUsers(f).Create(t.Context(), map[string]any{"name": "Kid"}); err == nil {
		t.Error("Create with a reply without user = nil error, want one")
	}
}
