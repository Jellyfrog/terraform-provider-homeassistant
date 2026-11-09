package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

var _ datasource.DataSourceWithConfigure = (*entityRegistryDataSource)(nil)

// NewEntityRegistry returns the `homeassistant_entity_registry` data source.
func NewEntityRegistry() datasource.DataSource {
	return &entityRegistryDataSource{}
}

type entityRegistryDataSource struct {
	registry client.EntityRegistry
}

type entityRegistryModel struct {
	DeviceID       types.String          `tfsdk:"device_id"`
	Domain         types.String          `tfsdk:"domain"`
	Platform       types.String          `tfsdk:"platform"`
	EntityCategory types.String          `tfsdk:"entity_category"`
	Entities       []entityRegistryEntry `tfsdk:"entities"`
}

// entityRegistryEntry is an element of `entities`.
type entityRegistryEntry struct {
	ID             types.String `tfsdk:"id"`
	EntityID       types.String `tfsdk:"entity_id"`
	UniqueID       types.String `tfsdk:"unique_id"`
	Platform       types.String `tfsdk:"platform"`
	DeviceID       types.String `tfsdk:"device_id"`
	EntityCategory types.String `tfsdk:"entity_category"`
	DisabledBy     types.String `tfsdk:"disabled_by"`
	HiddenBy       types.String `tfsdk:"hidden_by"`
}

func newEntityRegistryEntry(e client.EntityEntry) entityRegistryEntry {
	return entityRegistryEntry{
		ID:             types.StringValue(e.ID),
		EntityID:       types.StringValue(e.EntityID),
		UniqueID:       types.StringValue(string(e.UniqueID)),
		Platform:       types.StringValue(e.Platform),
		DeviceID:       types.StringPointerValue(e.DeviceID),
		EntityCategory: types.StringPointerValue(e.EntityCategory),
		DisabledBy:     types.StringPointerValue(e.DisabledBy),
		HiddenBy:       types.StringPointerValue(e.HiddenBy),
	}
}

func (d *entityRegistryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_entity_registry"
}

func (d *entityRegistryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Description: desc, Computed: true}
	}
	resp.Schema = schema.Schema{
		Description: "Lists the entries of the entity registry that match every filter that is set " +
			"(`config/entity_registry/list`). Without filters, it lists every entry. Unlike " +
			"`homeassistant_entities`, it includes disabled entities, and it has no state or " +
			"attributes. Filters compare exactly.",
		Attributes: map[string]schema.Attribute{
			"device_id": schema.StringAttribute{
				Description: "Only entities of the device with this ID.",
				Optional:    true,
			},
			"domain": schema.StringAttribute{
				Description: "Only entities of this domain, e.g. `light`.",
				Optional:    true,
			},
			"platform": schema.StringAttribute{
				Description: "Only entities of this integration, e.g. `mqtt`.",
				Optional:    true,
			},
			"entity_category": schema.StringAttribute{
				Description: "Only entities of this category: `config` or `diagnostic`.",
				Optional:    true,
				Validators:  []validator.String{stringvalidator.OneOf("config", "diagnostic")},
			},
			"entities": schema.ListNestedAttribute{
				Description: "The matching entries, sorted by `entity_id`.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":        computed("Registry ID. Unlike the entity ID, a rename does not change it."),
					"entity_id": computed("Entity ID."),
					"unique_id": computed("ID the integration gave the entity, unique within the " +
						"integration."),
					"platform":  computed("Integration that provides the entity, e.g. `mqtt`."),
					"device_id": computed("ID of the entity's device; null if it belongs to none."),
					"entity_category": computed("`config` or `diagnostic`; null for a primary " +
						"entity."),
					"disabled_by": computed("Who disabled the entity, e.g. `user` or " +
						"`integration`; null if it is enabled."),
					"hidden_by": computed("Who hid the entity, e.g. `user` or `integration`; null " +
						"if it is visible."),
				}},
			},
		},
	}
}

// Configure takes the entity registry (over the WebSocket) from the provider.
func (d *entityRegistryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if c := haClient(req, resp); c != nil {
		d.registry = client.Entities(c.WS)
	}
}

// Read lists the entity registry once and applies the filters to it.
func (d *entityRegistryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg entityRegistryModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entries, err := d.registry.List(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading the entity registry", client.ErrorDetail(err))
		return
	}
	f := entityRegistryFilter{
		deviceID:       cfg.DeviceID.ValueStringPointer(),
		domain:         cfg.Domain.ValueStringPointer(),
		platform:       cfg.Platform.ValueStringPointer(),
		entityCategory: cfg.EntityCategory.ValueStringPointer(),
	}
	matched := f.apply(entries)
	cfg.Entities = make([]entityRegistryEntry, len(matched))
	for i, e := range matched {
		cfg.Entities[i] = newEntityRegistryEntry(e)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}

// entityRegistryFilter holds the filters of the entity registry data source. Nil fields don't
// filter.
type entityRegistryFilter struct {
	deviceID, domain, platform, entityCategory *string
}

// apply returns the entries that pass every filter, sorted by entity ID.
func (f entityRegistryFilter) apply(entries []client.EntityEntry) []client.EntityEntry {
	return filterSorted(entries, f.matches, func(e client.EntityEntry) string { return e.EntityID })
}

// matches reports whether e passes every filter that is set.
func (f entityRegistryFilter) matches(e client.EntityEntry) bool {
	if f.domain != nil && !inDomain(e.EntityID, *f.domain) {
		return false
	}
	return ptrMatches(f.platform, &e.Platform) && ptrMatches(f.deviceID, e.DeviceID) &&
		ptrMatches(f.entityCategory, e.EntityCategory)
}
