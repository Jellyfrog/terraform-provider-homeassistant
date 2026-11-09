package resources

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/text/cases"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*userResource)(nil)
	_ resource.ResourceWithImportState = (*userResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*userResource)(nil)
)

// NewUser returns the `homeassistant_user` resource.
func NewUser() resource.Resource {
	return &userResource{}
}

type userResource struct {
	users client.Users
}

type userModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	// PasswordWO is write-only: it is null in plan and state, and set only in the config.
	PasswordWO        types.String `tfsdk:"password_wo"`
	PasswordWOVersion types.Int64  `tfsdk:"password_wo_version"`
	Group             types.String `tfsdk:"group"`
	IsActive          types.Bool   `tfsdk:"is_active"`
	LocalOnly         types.Bool   `tfsdk:"local_only"`
}

// password returns the password that creates a login: `password`, or the write-only
// `password_wo` from the config. It is null if neither is set.
func (m userModel) password(config userModel) types.String {
	if !m.Password.IsNull() {
		return m.Password
	}
	return config.PasswordWO
}

// userGroups maps the `group` attribute to Home Assistant's system groups.
var userGroups = map[string]string{
	"admin":     client.GroupAdmin,
	"user":      client.GroupUser,
	"read-only": client.GroupReadOnly,
}

// groupOf returns the `group` value for the group IDs of a user, which must be exactly one
// system group.
func groupOf(groupIDs []string) (string, error) {
	if len(groupIDs) == 1 {
		for name, id := range userGroups {
			if groupIDs[0] == id {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("the user is in the groups %q; the provider manages only users in exactly one of "+
		"%q, %q and %q", groupIDs, client.GroupAdmin, client.GroupUser, client.GroupReadOnly)
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Home Assistant user, optionally with a username and password to log in with. " +
			"The owner and system-generated users cannot be managed. Changing the username or password " +
			"of an existing user needs the token of the owner, not only of an admin. Create a " +
			"`homeassistant_person` with this user's `id` to track the user's presence.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "User ID that Home Assistant assigned, a random hex string. Import with this ID " +
					"or with the username.",
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Display name.",
				Required:    true,
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"username": schema.StringAttribute{
				Description: "Username to log in with. It must be lowercase and have no spaces at the start or " +
					"end. Without it, the user has no password login. Removing it from the configuration " +
					"deletes the login; the plan warns about it. Changing it renames the login in place and " +
					"needs the owner's token. Creating a login needs `password` or `password_wo`.",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					usernameValidator{},
				},
			},
			"password": schema.StringAttribute{
				Description: "Password to log in with. It is kept in the state, so protect the state. Home " +
					"Assistant cannot report the password, so a change made outside OpenTofu is not detected. " +
					"Creating a login needs it or `password_wo`. Without either, the provider leaves the password " +
					"of an existing login as it is, e.g. after an import. Changing the password of an existing " +
					"login needs the owner's token. Needs `username`. To keep the password out of the state, use " +
					"`password_wo` instead.",
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.AlsoRequires(path.MatchRoot("username")),
					stringvalidator.ConflictsWith(path.MatchRoot("password_wo")),
				},
			},
			"password_wo": schema.StringAttribute{
				Description: "Password to log in with, as a write-only attribute: OpenTofu passes it to the " +
					"provider but never stores it in the plan or state. Needs OpenTofu or Terraform 1.11 or " +
					"later; with older versions, use `password`. Because the provider cannot see a change of a " +
					"write-only value, it sets the password when the login is created and whenever " +
					"`password_wo_version` changes. Changing the password of an existing login needs the " +
					"owner's token. Needs `username`.",
				Optional:   true,
				Sensitive:  true,
				WriteOnly:  true,
				Validators: []validator.String{stringvalidator.AlsoRequires(path.MatchRoot("username"))},
			},
			"password_wo_version": schema.Int64Attribute{
				Description: "Any number that changes when `password_wo` changes, e.g. `1`, then `2`. Changing it " +
					"sets the password from `password_wo`.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.AlsoRequires(path.MatchRoot("password_wo"))},
			},
			"group": schema.StringAttribute{
				Description: "What the user may do: `admin`, `user` (the default), or `read-only`.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("user"),
				Validators:  []validator.String{stringvalidator.OneOf("admin", "user", "read-only")},
			},
			"is_active": schema.BoolAttribute{
				Description: "Whether the user can log in. Defaults to `true`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"local_only": schema.BoolAttribute{
				Description: "Whether the user can log in only from the local network. Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
		},
	}
}

// usernameValidator accepts usernames that Home Assistant stores unchanged: it rejects new
// usernames that are not lowercase or have spaces at the start or end.
type usernameValidator struct{}

var _ validator.String = usernameValidator{}

func (usernameValidator) Description(context.Context) string {
	return "must be lowercase, without spaces at the start or end"
}

func (v usernameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v usernameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	// Home Assistant requires username == username.strip().casefold(); Unicode case folding is
	// what Python's casefold does, e.g. "ß" becomes "ss", which lowercasing keeps.
	if strings.TrimSpace(s) != s || cases.Fold().String(s) != s {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Attribute Value",
			fmt.Sprintf("Attribute %s %s, got: %q", req.Path, v.Description(ctx), s))
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := haClient(req, resp); c != nil {
		r.users = client.NewUsers(c.WS)
	}
}

// fields are the settable fields of the user other than `is_active`, as create takes them.
func (m userModel) fields() map[string]any {
	return map[string]any{
		"name":       m.Name.ValueString(),
		"group_ids":  []string{userGroups[m.Group.ValueString()]},
		"local_only": m.LocalOnly.ValueBool(),
	}
}

// missingPassword adds an error to diags if creating the login username has no password, which
// Home Assistant would otherwise accept as an empty one, and returns whether it did. password is
// the result of userModel.password.
func missingPassword(username, password types.String, diags *diag.Diagnostics) bool {
	if username.IsNull() || !password.IsNull() {
		return false
	}
	diags.AddAttributeError(path.Root("password"), "Missing password",
		fmt.Sprintf("Creating the login %q needs a password. Set `password` or `password_wo`.", username.ValueString()))
	return true
}

// ownerHint adds to an error from changing a username or password that only the owner may.
func ownerHint(err error) string {
	detail := client.ErrorDetail(err)
	var wsErr *client.WSError
	if errors.As(err, &wsErr) && wsErr.Code == "unauthorized" {
		detail += ". Only the owner of Home Assistant may change the username or password of an " +
			"existing user: configure the provider with a token of the owner"
	}
	return detail
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	password := plan.password(config)
	// The plan checks this too, but not while username is unknown.
	if resp.Diagnostics.HasError() || missingPassword(plan.Username, password, &resp.Diagnostics) {
		return
	}

	u, err := r.users.Create(ctx, plan.fields())
	if err != nil {
		resp.Diagnostics.AddError("Creating user", client.ErrorDetail(err))
		return
	}

	// The rest runs on the new user; if it fails, the user is deleted again, even if ctx was
	// cancelled, so no half-made user is left behind.
	undo := func(summary string, err error) {
		detail := client.ErrorDetail(err)
		if delErr := r.users.Delete(context.WithoutCancel(ctx), u.ID); delErr != nil {
			detail += fmt.Sprintf(". Deleting the new user %s again also failed: %s. Delete it in Home Assistant",
				u.ID, client.ErrorDetail(delErr))
		} else {
			detail += ". The new user was deleted again"
		}
		resp.Diagnostics.AddError(summary, detail)
	}
	if !plan.IsActive.ValueBool() {
		if _, err := r.users.Update(ctx, u.ID, map[string]any{"is_active": false}); err != nil {
			undo("Deactivating user", err)
			return
		}
	}
	if !plan.Username.IsNull() {
		if err := r.users.CreateCredentials(ctx, u.ID, plan.Username.ValueString(), password.ValueString()); err != nil {
			undo("Creating login", err)
			return
		}
	}

	plan.ID = types.StringValue(u.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// ModifyPlan rejects creating a login without a password, which Home Assistant needs, and warns
// when the plan deletes a login.
func (r *userResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, config userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	var state userModel
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	hasLogin := !state.Username.IsNull()
	switch {
	case !hasLogin && !plan.Username.IsUnknown():
		missingPassword(plan.Username, plan.password(config), &resp.Diagnostics)
	case hasLogin && plan.Username.IsNull():
		resp.Diagnostics.AddAttributeWarning(path.Root("username"), "Login will be deleted",
			fmt.Sprintf("`username` is not set, so applying deletes the login %q of user %s, and it can no "+
				"longer log in with a password. Set `username = %q` to keep it.",
				state.Username.ValueString(), state.ID.ValueString(), state.Username.ValueString()))
	}
}

// managed adds an error and returns false for users the provider cannot manage.
func managed(u client.User, diags *diag.Diagnostics) bool {
	switch {
	case u.SystemGenerated:
		diags.AddError("User is system-generated",
			fmt.Sprintf("User %s (%q) is generated by Home Assistant and cannot be managed.", u.ID, u.Name))
		return false
	case u.IsOwner:
		diags.AddError("User is the owner",
			fmt.Sprintf("User %s (%q) is the owner of Home Assistant. The provider cannot manage the "+
				"owner.", u.ID, u.Name))
		return false
	}
	return true
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	u, ok, err := r.users.Get(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading user", client.ErrorDetail(err))
		return
	}
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	if !managed(u, &resp.Diagnostics) {
		return
	}
	group, err := groupOf(u.GroupIDs)
	if err != nil {
		resp.Diagnostics.AddError("Reading user", fmt.Sprintf("User %s: %s.", u.ID, err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, userModel{
		ID:                types.StringValue(u.ID),
		Name:              types.StringValue(u.Name),
		Username:          types.StringPointerValue(u.Username),
		Password:          passwordFor(u.Username, state),
		PasswordWO:        types.StringNull(),
		PasswordWOVersion: state.PasswordWOVersion,
		Group:             types.StringValue(group),
		IsActive:          types.BoolValue(u.IsActive),
		LocalOnly:         types.BoolValue(u.LocalOnly),
	})...)
}

// passwordFor returns the password to keep in state for a user whose login is username. Home
// Assistant cannot report it, so it is the one in state, unless the login is gone.
func passwordFor(username *string, state userModel) types.String {
	if username == nil {
		return types.StringNull()
	}
	return state.Password
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	password := plan.password(config)

	fields := plan.fields()
	fields["is_active"] = plan.IsActive.ValueBool()
	if _, err := r.users.Update(ctx, id, fields); err != nil {
		resp.Diagnostics.AddError("Updating user", client.ErrorDetail(err))
		return
	}
	// The state now holds the new settings, so a failure below does not apply them again.
	saved := state
	saved.Name, saved.Group, saved.IsActive, saved.LocalOnly = plan.Name, plan.Group, plan.IsActive, plan.LocalOnly
	resp.Diagnostics.Append(resp.State.Set(ctx, saved)...)

	oldName, newName := state.Username, plan.Username
	switch {
	case oldName.IsNull() && !newName.IsNull():
		if missingPassword(newName, password, &resp.Diagnostics) || !r.loginIs(ctx, id, nil, &resp.Diagnostics) {
			return
		}
		if err := r.users.CreateCredentials(ctx, id, newName.ValueString(), password.ValueString()); err != nil {
			resp.Diagnostics.AddError("Creating login", client.ErrorDetail(err))
			return
		}
	case !oldName.IsNull() && newName.IsNull():
		if !r.loginIs(ctx, id, oldName.ValueStringPointer(), &resp.Diagnostics) {
			return
		}
		if err := r.users.DeleteCredentials(ctx, oldName.ValueString()); err != nil {
			resp.Diagnostics.AddError("Deleting login", client.ErrorDetail(err))
			return
		}
	case !oldName.IsNull():
		if !oldName.Equal(newName) {
			if err := r.users.ChangeUsername(ctx, id, newName.ValueString()); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("username"), "Changing username", ownerHint(err))
				return
			}
			saved.Username = newName
			resp.Diagnostics.Append(resp.State.Set(ctx, saved)...)
		}
		if passwordChanged(plan, state, config) {
			if err := r.users.ChangePassword(ctx, id, password.ValueString()); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("password"), "Changing password", ownerHint(err))
				return
			}
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// loginIs reports whether the user with the given ID has the login want (nil for none) in Home
// Assistant, and adds an error otherwise. A plan made before the login changed outside OpenTofu
// must not attach a second login (Home Assistant before 2026.11 allows it), nor delete a login by a
// username that now belongs to another user.
func (r *userResource) loginIs(ctx context.Context, id string, want *string, diags *diag.Diagnostics) bool {
	u, ok, err := r.users.Get(ctx, id)
	switch {
	case err != nil:
		diags.AddError("Reading user", client.ErrorDetail(err))
		return false
	case !ok:
		diags.AddError("User is gone", fmt.Sprintf("User %s was deleted outside OpenTofu. Plan again.", id))
		return false
	case (u.Username == nil) != (want == nil) || (want != nil && *u.Username != *want):
		diags.AddAttributeError(path.Root("username"), "Login changed outside OpenTofu",
			fmt.Sprintf("The login of user %s is %s in Home Assistant, but the plan expected %s. Plan again.",
				id, loginName(u.Username), loginName(want)))
		return false
	}
	return true
}

// loginName describes a login for a message.
func loginName(username *string) string {
	if username == nil {
		return "none"
	}
	return fmt.Sprintf("%q", *username)
}

// passwordChanged reports whether an update sets the password of an existing login: `password`
// differs from the state, or `password_wo` is set and `password_wo_version` changed. Neither set
// leaves the password as it is.
func passwordChanged(plan, state, config userModel) bool {
	if !plan.Password.IsNull() {
		return !state.Password.Equal(plan.Password)
	}
	return !config.PasswordWO.IsNull() && !state.PasswordWOVersion.Equal(plan.PasswordWOVersion)
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.users.Delete(ctx, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Deleting user", client.ErrorDetail(err))
	}
}

// ImportState takes the user's ID or username.
func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	users, err := r.users.List(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading users", client.ErrorDetail(err))
		return
	}
	i := slices.IndexFunc(users, func(u client.User) bool { return u.ID == req.ID })
	if i < 0 {
		i = slices.IndexFunc(users, func(u client.User) bool { return u.Username != nil && *u.Username == req.ID })
	}
	if i < 0 {
		resp.Diagnostics.AddError("User not found", fmt.Sprintf("No user has the ID or username %q.", req.ID))
		return
	}
	// Read, which runs next, rejects the owner and system-generated users.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), users[i].ID)...)
}
