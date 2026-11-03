package resources

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.ResourceWithConfigure   = (*lovelaceResourceResource)(nil)
	_ resource.ResourceWithImportState = (*lovelaceResourceResource)(nil)
)

// NewLovelaceResource returns the `homeassistant_lovelace_resource` resource.
func NewLovelaceResource() resource.Resource {
	return &lovelaceResourceResource{}
}

type lovelaceResourceResource struct {
	resources client.Registry[client.LovelaceResource]
}

type lovelaceResourceModel struct {
	ID   types.String `tfsdk:"id"`
	URL  types.String `tfsdk:"url"`
	Type types.String `tfsdk:"type"`
}

func newLovelaceResourceModel(r client.LovelaceResource) lovelaceResourceModel {
	return lovelaceResourceModel{
		ID:   types.StringValue(r.ID),
		URL:  types.StringValue(r.URL),
		Type: types.StringValue(r.Type),
	}
}

// fields are the settable fields, as create and update take them.
func (m lovelaceResourceModel) fields() map[string]any {
	return map[string]any{"res_type": m.Type.ValueString(), "url": m.URL.ValueString()}
}

func (r *lovelaceResourceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lovelace_resource"
}

func (r *lovelaceResourceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Lovelace resource: a URL that the frontend loads on every dashboard, such as the " +
			"JavaScript of a custom card. Home Assistant must keep its resources in storage (the default), " +
			"not in YAML.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "ID that Home Assistant assigned to the resource. Import with this ID or with the URL.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"url": schema.StringAttribute{
				Description: "URL of the file, e.g. `/hacsfiles/button-card/button-card.js` or " +
					"`/local/my-card.js`. Changing it updates the resource in place. Creating or " +
					"updating fails if another resource has this URL; import that one instead.",
				Required: true,
			},
			"type": schema.StringAttribute{
				Description: "How the frontend loads it: `module` (a JavaScript module, what most custom " +
					"cards are), `js` (a classic script), or `css` (a stylesheet).",
				Required:   true,
				Validators: []validator.String{stringvalidator.OneOf("module", "js", "css")},
			},
		},
	}
}

func (r *lovelaceResourceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := haClient(req, resp); c != nil {
		r.resources = client.LovelaceResources(c.WS)
	}
}

func (r *lovelaceResourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan lovelaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.urlFree(ctx, plan.URL.ValueString(), "", &resp.Diagnostics) {
		return
	}

	created, err := r.resources.Create(ctx, plan.fields())
	if err != nil {
		resp.Diagnostics.AddError("Creating Lovelace resource", client.ErrorDetail(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newLovelaceResourceModel(created))...)
}

// urlFree reports whether no resource but the one with the ID self has the URL, and adds an
// error otherwise: Home Assistant would keep both, and the frontend would load the file twice.
func (r *lovelaceResourceResource) urlFree(ctx context.Context, url, self string, diags *diag.Diagnostics) bool {
	other, ok, err := r.resources.Find(ctx, func(l client.LovelaceResource) bool { return l.URL == url && l.ID != self })
	if err != nil {
		diags.AddError("Reading Lovelace resources", client.ErrorDetail(err))
		return false
	}
	if ok {
		diags.AddAttributeError(path.Root("url"), "Lovelace resource exists",
			fmt.Sprintf("A Lovelace resource with the URL %q exists already (ID %s). Import it, e.g. "+
				"`tofu import <address> %s`, or remove it in Home Assistant.", url, other.ID, other.ID))
		return false
	}
	return true
}

func (r *lovelaceResourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state lovelaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	l, ok, err := r.resources.Get(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading Lovelace resource", client.ErrorDetail(err))
		return
	}
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newLovelaceResourceModel(l))...)
}

func (r *lovelaceResourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan lovelaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.urlFree(ctx, plan.URL.ValueString(), plan.ID.ValueString(), &resp.Diagnostics) {
		return
	}

	updated, err := r.resources.Update(ctx, plan.ID.ValueString(), plan.fields())
	if err != nil {
		resp.Diagnostics.AddError("Updating Lovelace resource", client.ErrorDetail(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newLovelaceResourceModel(updated))...)
}

func (r *lovelaceResourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state lovelaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.resources.Delete(ctx, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Deleting Lovelace resource", client.ErrorDetail(err))
	}
}

// ImportState takes the resource's ID or its URL.
func (r *lovelaceResourceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	all, err := r.resources.List(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading Lovelace resources", client.ErrorDetail(err))
		return
	}
	var ids []string
	for _, l := range all {
		if l.ID == req.ID {
			ids = []string{l.ID}
			break
		}
		if l.URL == req.ID {
			ids = append(ids, l.ID)
		}
	}
	switch len(ids) {
	case 0:
		resp.Diagnostics.AddError("Lovelace resource not found",
			fmt.Sprintf("No Lovelace resource has the ID or URL %q.", req.ID))
	case 1:
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ids[0])...)
	default:
		resp.Diagnostics.AddError("Lovelace resource ambiguous",
			fmt.Sprintf("Several Lovelace resources have the URL %q (IDs %s). Import one by its ID.",
				req.ID, strings.Join(ids, ", ")))
	}
}
