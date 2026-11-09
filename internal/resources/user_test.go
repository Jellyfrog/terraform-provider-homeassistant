package resources

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// fakeUsers is a WSCommander serving users and their logins like Home Assistant. Commands whose
// type is in fail return an error with that code.
type fakeUsers struct {
	users     []client.User
	passwords map[string]string // by username
	fail      map[string]string
	calls     []string
	nextID    int
	// cancel, if set, is called when the command cancelOn runs, which then fails like a cancelled
	// command. Like the real client, a command on a cancelled context fails.
	cancelOn string
	cancel   context.CancelFunc
}

func (f *fakeUsers) Command(ctx context.Context, typ string, params map[string]any, result any) error {
	f.calls = append(f.calls, typ)
	if typ == f.cancelOn {
		f.cancel()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if code, ok := f.fail[typ]; ok {
		return &client.WSError{Code: code, Message: typ + " failed"}
	}
	reply := func(v any) error { return jsonReply(v, result) }
	find := func() *client.User {
		for i := range f.users {
			if f.users[i].ID == params["user_id"] {
				return &f.users[i]
			}
		}
		return nil
	}
	switch strings.TrimPrefix(typ, "config/") {
	case "auth/list":
		return reply(f.users)
	case "auth/create":
		f.nextID++
		u := client.User{ID: fmt.Sprintf("u%d", f.nextID), Name: params["name"].(string), IsActive: true}
		u.GroupIDs = params["group_ids"].([]string)
		u.LocalOnly, _ = params["local_only"].(bool)
		f.users = append(f.users, u)
		return reply(map[string]any{"user": u})
	case "auth/update":
		u := find()
		if u == nil {
			return &client.WSError{Code: "not_found", Message: "User not found"}
		}
		if v, ok := params["name"].(string); ok {
			u.Name = v
		}
		if v, ok := params["is_active"].(bool); ok {
			u.IsActive = v
		}
		if v, ok := params["local_only"].(bool); ok {
			u.LocalOnly = v
		}
		if v, ok := params["group_ids"].([]string); ok {
			u.GroupIDs = v
		}
		return reply(map[string]any{"user": u})
	case "auth/delete":
		if find() == nil {
			return &client.WSError{Code: "not_found", Message: "User not found"}
		}
		f.users = slices.DeleteFunc(f.users, func(u client.User) bool { return u.ID == params["user_id"] })
		return nil
	case "auth_provider/homeassistant/create":
		u := find()
		if u.Username != nil {
			return &client.WSError{Code: "credentials_exist", Message: "exists"}
		}
		name := params["username"].(string)
		u.Username = &name
		f.passwords[name] = params["password"].(string)
		return nil
	case "auth_provider/homeassistant/delete":
		name := params["username"].(string)
		delete(f.passwords, name)
		for i := range f.users {
			if f.users[i].Username != nil && *f.users[i].Username == name {
				f.users[i].Username = nil
			}
		}
		return nil
	case "auth_provider/homeassistant/admin_change_username":
		u := find()
		name := params["username"].(string)
		f.passwords[name] = f.passwords[*u.Username]
		delete(f.passwords, *u.Username)
		u.Username = &name
		return nil
	case "auth_provider/homeassistant/admin_change_password":
		f.passwords[*find().Username] = params["password"].(string)
		return nil
	}
	return &client.WSError{Code: "unknown_command", Message: typ}
}

func newFakeUsers(users ...client.User) *fakeUsers {
	return &fakeUsers{users: users, passwords: map[string]string{}}
}

func userRes(f *fakeUsers) *userResource {
	return &userResource{users: client.NewUsers(f)}
}

func jane() userModel {
	return userModel{
		ID: types.StringUnknown(), Name: types.StringValue("Jane"),
		Username: types.StringValue("jane"), Password: types.StringValue("secret"),
		Group: types.StringValue("admin"), IsActive: types.BoolValue(false), LocalOnly: types.BoolValue(true),
	}
}

// userRequest returns the plan and config of a configuration m: the config holds m, and the plan
// holds m without the write-only password_wo, as OpenTofu plans it.
func userRequest(t *testing.T, r *userResource, m userModel) (tfsdk.Plan, tfsdk.Config) {
	t.Helper()
	plan := m
	plan.PasswordWO = types.StringNull()
	return planOf(t, r, plan), tfsdk.Config(stateOf(t, r, m))
}

func createUser(t *testing.T, f *fakeUsers, m userModel) (userModel, resource.CreateResponse) {
	t.Helper()
	r := userRes(f)
	resp := resource.CreateResponse{State: stateOf(t, r, userModel{})}
	plan, config := userRequest(t, r, m)
	r.Create(t.Context(), resource.CreateRequest{Plan: plan, Config: config}, &resp)
	var got userModel
	if !resp.Diagnostics.HasError() {
		resp.State.Get(t.Context(), &got)
	}
	return got, resp
}

func readUser(t *testing.T, f *fakeUsers, m userModel) (userModel, resource.ReadResponse) {
	t.Helper()
	r := userRes(f)
	state := stateOf(t, r, m)
	resp := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	var got userModel
	if !resp.State.Raw.IsNull() {
		resp.State.Get(t.Context(), &got)
	}
	return got, resp
}

func updateUser(t *testing.T, f *fakeUsers, old, m userModel) (userModel, resource.UpdateResponse) {
	t.Helper()
	r := userRes(f)
	state := stateOf(t, r, old)
	resp := resource.UpdateResponse{State: state}
	plan, config := userRequest(t, r, m)
	r.Update(t.Context(), resource.UpdateRequest{Plan: plan, Config: config, State: state}, &resp)
	var got userModel
	resp.State.Get(t.Context(), &got)
	return got, resp
}

func TestUserCreateAndRead(t *testing.T) {
	f := newFakeUsers()
	created, resp := createUser(t, f, jane())
	checkDiags(t, resp.Diagnostics, "")
	if created.ID.ValueString() != "u1" || f.passwords["jane"] != "secret" {
		t.Fatalf("created %+v, passwords %v", created, f.passwords)
	}
	u := f.users[0]
	if u.IsActive || !u.LocalOnly || !slices.Equal(u.GroupIDs, []string{client.GroupAdmin}) {
		t.Errorf("in Home Assistant: %+v", u)
	}

	// Read reports everything back and keeps the password, which Home Assistant cannot report.
	got, rresp := readUser(t, f, created)
	checkDiags(t, rresp.Diagnostics, "")
	if got != created {
		t.Errorf("read = %+v, want %+v", got, created)
	}

	// The login deleted outside OpenTofu: username and password become null, which is drift.
	f.users[0].Username = nil
	got, _ = readUser(t, f, created)
	if !got.Username.IsNull() || !got.Password.IsNull() {
		t.Errorf("read without login: username %v, password %v; want both null", got.Username, got.Password)
	}
}

func TestUserCreateWithoutLogin(t *testing.T) {
	f := newFakeUsers()
	m := jane()
	m.Username, m.Password, m.IsActive = types.StringNull(), types.StringNull(), types.BoolValue(true)
	_, resp := createUser(t, f, m)
	checkDiags(t, resp.Diagnostics, "")
	if !slices.Equal(f.calls, []string{"config/auth/create"}) {
		t.Errorf("calls = %v, want only the create", f.calls)
	}
}

func TestUserCreateUndoneWhenLoginFails(t *testing.T) {
	f := newFakeUsers()
	f.fail = map[string]string{"config/auth_provider/homeassistant/create": "home_assistant_error"}
	_, resp := createUser(t, f, jane())
	checkDiags(t, resp.Diagnostics, "deleted again")
	if len(f.users) != 0 {
		t.Errorf("users left behind: %+v", f.users)
	}
}

func TestUserReadGone(t *testing.T) {
	m := jane()
	m.ID = types.StringValue("u1")
	_, resp := readUser(t, newFakeUsers(), m)
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("read of a deleted user: state null = %v, %v; want removed, no error", resp.State.Raw.IsNull(), resp.Diagnostics)
	}
}

func TestUserReadRejectsUnmanageable(t *testing.T) {
	for _, tc := range []struct {
		user client.User
		want string
	}{
		{client.User{ID: "u1", IsOwner: true, GroupIDs: []string{client.GroupAdmin}}, "owner"},
		{client.User{ID: "u1", SystemGenerated: true, GroupIDs: []string{client.GroupAdmin}}, "generated"},
		{client.User{ID: "u1", GroupIDs: []string{client.GroupAdmin, client.GroupUser}}, "exactly one"},
		{client.User{ID: "u1"}, "exactly one"},
	} {
		m := jane()
		m.ID = types.StringValue("u1")
		_, resp := readUser(t, newFakeUsers(tc.user), m)
		checkDiags(t, resp.Diagnostics, tc.want)
		if resp.State.Raw.IsNull() {
			t.Errorf("%+v: read removed the user from state", tc.user)
		}
	}
}

func TestUserUpdateLogin(t *testing.T) {
	same := func(m userModel) userModel { return m }
	noLogin := func(m userModel) userModel {
		m.Username, m.Password = types.StringNull(), types.StringNull()
		return m
	}
	renamed := func(m userModel) userModel {
		m.Username = types.StringValue("janet")
		return m
	}
	repassed := func(m userModel) userModel {
		m.Password = types.StringValue("secret2")
		return m
	}
	for _, tc := range []struct {
		name          string
		from, to      func(userModel) userModel
		wantCall      string
		wantPasswords map[string]string
	}{
		{"add", noLogin, same, "config/auth_provider/homeassistant/create", map[string]string{"jane": "secret"}},
		{"remove", same, noLogin, "config/auth_provider/homeassistant/delete", map[string]string{}},
		{"rename", same, renamed, "config/auth_provider/homeassistant/admin_change_username", map[string]string{"janet": "secret"}},
		{"password", same, repassed, "config/auth_provider/homeassistant/admin_change_password", map[string]string{"jane": "secret2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeUsers()
			created, _ := createUser(t, f, tc.from(jane()))
			f.calls = nil
			want := tc.to(jane())
			want.ID = created.ID
			got, resp := updateUser(t, f, created, want)
			checkDiags(t, resp.Diagnostics, "")
			wantCalls := []string{"config/auth/update", tc.wantCall}
			if tc.name == "add" || tc.name == "remove" {
				// Adding or removing a login checks the login in Home Assistant first.
				wantCalls = []string{"config/auth/update", "config/auth/list", tc.wantCall}
			}
			if !slices.Equal(f.calls, wantCalls) {
				t.Errorf("calls = %v", f.calls)
			}
			if fmt.Sprint(f.passwords) != fmt.Sprint(tc.wantPasswords) {
				t.Errorf("passwords = %v, want %v", f.passwords, tc.wantPasswords)
			}
			if got != want {
				t.Errorf("state = %+v, want %+v", got, want)
			}
		})
	}
}

func TestUserUpdatePasswordNeedsOwner(t *testing.T) {
	f := newFakeUsers()
	created, _ := createUser(t, f, jane())
	f.fail = map[string]string{"config/auth_provider/homeassistant/admin_change_password": "unauthorized"}
	m := jane()
	m.ID, m.Name, m.Password = created.ID, types.StringValue("Janet"), types.StringValue("secret2")
	got, resp := updateUser(t, f, created, m)
	checkDiags(t, resp.Diagnostics, "token of the owner")
	// The new name is applied and saved; the old password stays, so the next plan retries it.
	if got.Name.ValueString() != "Janet" || got.Password.ValueString() != "secret" {
		t.Errorf("state after the failed password change = %+v", got)
	}
}

func TestUserImport(t *testing.T) {
	f := newFakeUsers(
		client.User{ID: "u1", IsOwner: true, Username: ptr("owner")},
		client.User{ID: "u2", Username: ptr("jane")},
		client.User{ID: "u3", SystemGenerated: true},
	)
	for _, tc := range []struct{ id, wantID, wantErr string }{
		{"u2", "u2", ""},
		{"jane", "u2", ""},
		// Read, which runs after the import, rejects these two.
		{"owner", "u1", ""},
		{"u3", "u3", ""},
		{"nobody", "", "No user"},
	} {
		r := userRes(f)
		resp := resource.ImportStateResponse{State: stateOf(t, r, userModel{})}
		r.ImportState(t.Context(), resource.ImportStateRequest{ID: tc.id}, &resp)
		checkDiags(t, resp.Diagnostics, tc.wantErr)
		var id types.String
		resp.State.GetAttribute(t.Context(), path.Root("id"), &id)
		if id.ValueString() != tc.wantID {
			t.Errorf("import %q: id %q, want %q", tc.id, id.ValueString(), tc.wantID)
		}
	}
}

func TestUsernameValidator(t *testing.T) {
	for s, ok := range map[string]bool{"jane": true, "jane.doe@x": true, "Jane": false, " jane": false, "jane ": false, "straße": false} {
		var resp validator.StringResponse
		usernameValidator{}.ValidateString(t.Context(), validator.StringRequest{ConfigValue: types.StringValue(s)}, &resp)
		if resp.Diagnostics.HasError() == ok {
			t.Errorf("%q: errors %v, want ok = %v", s, resp.Diagnostics, ok)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func modifyUserPlan(t *testing.T, state *userModel, plan userModel) resource.ModifyPlanResponse {
	t.Helper()
	r := userRes(newFakeUsers())
	p, config := userRequest(t, r, plan)
	req := resource.ModifyPlanRequest{Plan: p, Config: config, State: stateOf(t, r, userModel{})}
	req.State.Raw = tftypes.NewValue(req.State.Schema.Type().TerraformType(t.Context()), nil)
	if state != nil {
		req.State = stateOf(t, r, *state)
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.ModifyPlan(t.Context(), req, &resp)
	return resp
}

func TestUserModifyPlan(t *testing.T) {
	withLogin := jane()
	withLogin.ID = types.StringValue("u1")
	noPassword := withLogin
	noPassword.Password = types.StringNull()
	noLogin := withLogin
	noLogin.Username, noLogin.Password = types.StringNull(), types.StringNull()

	// Creating a login needs a password, on a new user and on one without a login.
	checkDiags(t, modifyUserPlan(t, nil, noPassword).Diagnostics, "needs a password")
	checkDiags(t, modifyUserPlan(t, &noLogin, noPassword).Diagnostics, "needs a password")
	// An existing login keeps its password when none is set, e.g. after an import.
	checkDiags(t, modifyUserPlan(t, &withLogin, noPassword).Diagnostics, "")
	// Deleting a login warns.
	resp := modifyUserPlan(t, &withLogin, noLogin)
	checkDiags(t, resp.Diagnostics, "")
	if w := resp.Diagnostics.Warnings(); len(w) != 1 || !strings.Contains(w[0].Detail(), `login "jane"`) {
		t.Errorf("warnings = %v, want one about deleting the login", w)
	}
}

func TestUserUpdateWithoutPasswordKeepsIt(t *testing.T) {
	f := newFakeUsers()
	created, _ := createUser(t, f, jane())
	f.calls = nil
	m := created
	m.Password = types.StringNull()
	_, resp := updateUser(t, f, created, m)
	checkDiags(t, resp.Diagnostics, "")
	if slices.Contains(f.calls, "config/auth_provider/homeassistant/admin_change_password") || f.passwords["jane"] != "secret" {
		t.Errorf("calls = %v, passwords = %v; want the password unchanged", f.calls, f.passwords)
	}
}

// A username that is unknown during the plan skips the plan's password check, so apply checks
// again instead of creating a login with an empty password.
func TestUserApplyNeedsPasswordForNewLogin(t *testing.T) {
	m := jane()
	m.Password = types.StringNull()
	f := newFakeUsers()
	_, resp := createUser(t, f, m)
	checkDiags(t, resp.Diagnostics, "needs a password")
	if len(f.calls) != 0 {
		t.Errorf("create: calls = %v, want none", f.calls)
	}

	noLogin := jane()
	noLogin.Username, noLogin.Password = types.StringNull(), types.StringNull()
	created, _ := createUser(t, f, noLogin)
	f.calls = nil
	m.ID = created.ID
	_, uresp := updateUser(t, f, created, m)
	checkDiags(t, uresp.Diagnostics, "needs a password")
	if slices.Contains(f.calls, "config/auth_provider/homeassistant/create") {
		t.Errorf("update: calls = %v, want no login created", f.calls)
	}
}

// withWO returns m with its password as the write-only password_wo at the given version.
func withWO(m userModel, password string, version int64) userModel {
	m.Password = types.StringNull()
	m.PasswordWO = types.StringValue(password)
	m.PasswordWOVersion = types.Int64Value(version)
	return m
}

func TestUserWriteOnlyPassword(t *testing.T) {
	f := newFakeUsers()
	created, resp := createUser(t, f, withWO(jane(), "wo-secret", 1))
	checkDiags(t, resp.Diagnostics, "")
	if f.passwords["jane"] != "wo-secret" {
		t.Fatalf("passwords = %v, want the write-only one", f.passwords)
	}
	if !created.Password.IsNull() || !created.PasswordWO.IsNull() || created.PasswordWOVersion.ValueInt64() != 1 {
		t.Errorf("state = %+v, want no password and version 1", created)
	}

	// Read keeps the version, and the write-only password stays out of the state.
	got, rresp := readUser(t, f, created)
	checkDiags(t, rresp.Diagnostics, "")
	if got != created {
		t.Errorf("read = %+v, want %+v", got, created)
	}

	for _, tc := range []struct {
		name     string
		m        userModel
		wantCall bool
		wantPass string
	}{
		// A new value without a new version is not seen: the provider cannot compare it.
		{"same version", withWO(jane(), "wo-other", 1), false, "wo-secret"},
		{"new version", withWO(jane(), "wo-new", 2), true, "wo-new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.calls = nil
			tc.m.ID = created.ID
			_, uresp := updateUser(t, f, created, tc.m)
			checkDiags(t, uresp.Diagnostics, "")
			called := slices.Contains(f.calls, "config/auth_provider/homeassistant/admin_change_password")
			if called != tc.wantCall || f.passwords["jane"] != tc.wantPass {
				t.Errorf("change_password called = %v, passwords = %v", called, f.passwords)
			}
		})
	}
}

func TestUserSwitchToWriteOnlyPassword(t *testing.T) {
	f := newFakeUsers()
	created, _ := createUser(t, f, jane())
	m := withWO(jane(), "wo-secret", 1)
	m.ID = created.ID
	got, resp := updateUser(t, f, created, m)
	checkDiags(t, resp.Diagnostics, "")
	if f.passwords["jane"] != "wo-secret" || !got.Password.IsNull() {
		t.Errorf("passwords = %v, state password = %v; want the write-only one set and none in state", f.passwords, got.Password)
	}
}

func TestUserModifyPlanAcceptsWriteOnlyPassword(t *testing.T) {
	checkDiags(t, modifyUserPlan(t, nil, withWO(jane(), "wo-secret", 1)).Diagnostics, "")
}

func TestUserCreateUndoneWhenCancelled(t *testing.T) {
	for _, step := range []string{"config/auth/update", "config/auth_provider/homeassistant/create"} {
		t.Run(step, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f := newFakeUsers()
			f.cancelOn, f.cancel = step, cancel
			r := userRes(f)
			plan, config := userRequest(t, r, jane()) // jane is inactive, so create also updates
			resp := resource.CreateResponse{State: stateOf(t, r, userModel{})}
			r.Create(ctx, resource.CreateRequest{Plan: plan, Config: config}, &resp)
			checkDiags(t, resp.Diagnostics, "deleted again")
			if len(f.users) != 0 {
				t.Errorf("users left behind after Ctrl-C: %+v", f.users)
			}
		})
	}
}

func TestUserUpdateRejectsLoginChangedOutside(t *testing.T) {
	noLogin := jane()
	noLogin.Username, noLogin.Password = types.StringNull(), types.StringNull()

	t.Run("added", func(t *testing.T) {
		// Planned without a login; one was added in Home Assistant since.
		f := newFakeUsers()
		created, _ := createUser(t, f, noLogin)
		f.users[0].Username = ptr("jane-elsewhere")
		m := jane()
		m.ID = created.ID
		_, resp := updateUser(t, f, created, m)
		checkDiags(t, resp.Diagnostics, `is "jane-elsewhere" in Home Assistant, but the plan expected none`)
		if slices.Contains(f.calls, "config/auth_provider/homeassistant/create") {
			t.Error("a second login was created")
		}
	})
	t.Run("renamed", func(t *testing.T) {
		// Planned to delete the login "jane", which was renamed and given to someone else since.
		f := newFakeUsers()
		created, _ := createUser(t, f, jane())
		f.users[0].Username = ptr("janet")
		m := noLogin
		m.ID = created.ID
		_, resp := updateUser(t, f, created, m)
		checkDiags(t, resp.Diagnostics, `is "janet" in Home Assistant, but the plan expected "jane"`)
		if slices.Contains(f.calls, "config/auth_provider/homeassistant/delete") {
			t.Error("a login was deleted")
		}
	})
}
