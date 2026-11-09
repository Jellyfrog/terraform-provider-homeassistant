package resources

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*personResource)(nil)
	_ resource.ResourceWithImportState = (*personResource)(nil)
)

// NewPerson returns the `homeassistant_person` resource.
func NewPerson() resource.Resource {
	return &personResource{}
}

// deviceTrackerEntityID matches the entity ID of a device tracker, as Home Assistant validates it.
var deviceTrackerEntityID = regexp.MustCompile(`^device_tracker\.[a-z0-9_]+$`)

type personResource struct {
	persons client.Persons
}

type personModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	UserID         types.String `tfsdk:"user_id"`
	DeviceTrackers types.Set    `tfsdk:"device_trackers"`
	Picture        types.String `tfsdk:"picture"`
}

func newPersonModel(p client.Person) personModel {
	trackers := make([]attr.Value, 0, len(p.DeviceTrackers))
	for _, t := range p.DeviceTrackers {
		trackers = append(trackers, types.StringValue(t))
	}
	return personModel{
		ID:             types.StringValue(p.ID),
		Name:           types.StringValue(p.Name),
		UserID:         types.StringPointerValue(p.UserID),
		DeviceTrackers: types.SetValueMust(types.StringType, trackers),
		Picture:        types.StringPointerValue(p.Picture),
	}
}

// fields are every settable field of the person, with the given name. person/update resets the
// device trackers when they are left out, so creates and updates always build their fields here.
// Unset fields are null, which clears them; device trackers are sorted, so the call does not
// depend on the set's order.
func (m personModel) fields(name string) map[string]any {
	trackers := make([]string, 0, len(m.DeviceTrackers.Elements()))
	for _, t := range m.DeviceTrackers.Elements() {
		trackers = append(trackers, t.(types.String).ValueString())
	}
	slices.Sort(trackers)
	return map[string]any{
		"name":            name,
		"user_id":         m.UserID.ValueStringPointer(),
		"device_trackers": trackers,
		"picture":         m.Picture.ValueStringPointer(),
	}
}

func (r *personResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_person"
}

func (r *personResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A person: someone whose presence Home Assistant tracks with device trackers, " +
			"optionally linked to a user. Persons defined in YAML cannot be managed.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Person ID, a slug such as `jane_doe`. Home Assistant derives it from `name` " +
					"when the person is created, and keeps it when the person is renamed. Set it to choose " +
					"the ID yourself. Changing it replaces the person. Creation fails if the ID is taken. " +
					"Import with this ID.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
				Validators: []validator.String{slugValidator{}},
			},
			"name": schema.StringAttribute{
				Description: "Display name. Changing it renames the person in place.",
				Required:    true,
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"user_id": schema.StringAttribute{
				Description: "ID of the user this person is, e.g. `homeassistant_user.jane.id`. A user can " +
					"belong to only one person. When the user is deleted, Home Assistant unlinks it.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"device_trackers": schema.SetAttribute{
				Description: "Entity IDs of the `device_tracker` entities that track the person. Home " +
					"Assistant removes a tracker from the person when its entity is deleted. Defaults to none.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     emptyStringSet(),
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(deviceTrackerEntityID,
						"must be the entity ID of a device tracker, `device_tracker.<object_id>`")),
				},
			},
			"picture": schema.StringAttribute{
				Description: "URL of the picture, e.g. `/api/image/serve/<id>/512x512` for an image uploaded " +
					"to Home Assistant.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
		},
	}
}

func (r *personResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := haClient(req, resp); c != nil {
		r.persons = client.NewPersons(c.WS)
	}
}

func (r *personResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan personModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	g := generatedID{
		kind:     "person",
		typeName: "homeassistant_person",
		create: func(ctx context.Context, name string) (string, error) {
			p, err := r.persons.Create(ctx, withoutNulls(plan.fields(name)))
			return p.ID, err
		},
		rename: func(ctx context.Context, id, name string) error {
			_, err := r.persons.Update(ctx, id, plan.fields(name))
			return err
		},
		delete: r.persons.Delete,
	}
	id, err := g.Create(ctx, plan.Name.ValueString(), plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Creating person", client.ErrorDetail(err))
		return
	}

	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *personResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state personModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	p, ok, yaml, err := r.persons.Get(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading person", client.ErrorDetail(err))
		return
	}
	if yaml {
		resp.Diagnostics.AddError("Person is defined in YAML",
			fmt.Sprintf("Person %q is defined in the YAML configuration of Home Assistant, which the "+
				"provider cannot change. Remove it from YAML to manage it here.", state.ID.ValueString()))
		return
	}
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newPersonModel(p))...)
}

func (r *personResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan personModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.persons.Update(ctx, plan.ID.ValueString(), plan.fields(plan.Name.ValueString())); err != nil {
		resp.Diagnostics.AddError("Updating person", client.ErrorDetail(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *personResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state personModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.persons.Delete(ctx, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Deleting person", client.ErrorDetail(err))
	}
}

// ImportState takes the person's ID. Read, which runs next, rejects a person defined in YAML.
func (r *personResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
