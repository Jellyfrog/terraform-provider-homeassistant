package datasources

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

var (
	_ datasource.DataSourceWithConfigure      = (*areasDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*areaDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*areaDataSource)(nil)
)

// areaModel is one area, as `homeassistant_area` holds it and as an element of `areas`.
type areaModel struct {
	AreaID  types.String `tfsdk:"area_id"`
	Name    types.String `tfsdk:"name"`
	FloorID types.String `tfsdk:"floor_id"`
	Icon    types.String `tfsdk:"icon"`
	Aliases types.Set    `tfsdk:"aliases"`
	Labels  types.Set    `tfsdk:"labels"`
}

func newAreaModel(a client.Area) areaModel {
	return areaModel{
		AreaID:  types.StringValue(a.AreaID),
		Name:    types.StringValue(a.Name),
		FloorID: types.StringPointerValue(a.FloorID),
		Icon:    types.StringPointerValue(a.Icon),
		Aliases: stringSet(a.Aliases),
		Labels:  stringSet(a.Labels),
	}
}

// areaFields describes the attributes of an area other than area_id and name.
var areaFields = map[string]string{
	"floor_id": "ID of the floor the area is on; null if it is on none.",
	"icon":     "Icon, e.g. `mdi:sofa`; null if it has none.",
	"aliases":  "Other names voice assistants accept for the area.",
	"labels":   "IDs of the area's labels.",
}

// computedAreaAttributes returns the schema of areaFields, all computed.
func computedAreaAttributes() map[string]schema.Attribute {
	attrs := make(map[string]schema.Attribute, len(areaFields))
	for name, desc := range areaFields {
		if name == "aliases" || name == "labels" {
			attrs[name] = schema.SetAttribute{Description: desc, ElementType: types.StringType, Computed: true}
		} else {
			attrs[name] = schema.StringAttribute{Description: desc, Computed: true}
		}
	}
	return attrs
}

// areaRegistryClient returns the area registry from the provider data, or nil before the
// provider is configured or on error.
func areaRegistryClient(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *client.Registry[client.Area] {
	c := haClient(req, resp)
	if c == nil {
		return nil
	}
	r := client.Areas(c.WS)
	return &r
}

// NewAreas returns the `homeassistant_areas` data source.
func NewAreas() datasource.DataSource {
	return &areasDataSource{}
}

type areasDataSource struct {
	areas *client.Registry[client.Area]
}

type areasModel struct {
	Areas []areaModel `tfsdk:"areas"`
}

func (d *areasDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_areas"
}

func (d *areasDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := computedAreaAttributes()
	attrs["area_id"] = schema.StringAttribute{Description: "Area ID.", Computed: true}
	attrs["name"] = schema.StringAttribute{Description: "Name.", Computed: true}
	resp.Schema = schema.Schema{
		Description: "Lists every area (`config/area_registry/list`).",
		Attributes: map[string]schema.Attribute{
			"areas": schema.ListNestedAttribute{
				Description:  "The areas, sorted by `area_id`.",
				Computed:     true,
				NestedObject: schema.NestedAttributeObject{Attributes: attrs},
			},
		},
	}
}

func (d *areasDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if r := areaRegistryClient(req, resp); r != nil {
		d.areas = r
	}
}

func (d *areasDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	areas, err := d.areas.List(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading areas", client.ErrorDetail(err))
		return
	}
	sort.Slice(areas, func(i, j int) bool { return areas[i].AreaID < areas[j].AreaID })
	out := areasModel{Areas: make([]areaModel, len(areas))}
	for i, a := range areas {
		out.Areas[i] = newAreaModel(a)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}

// NewArea returns the `homeassistant_area` data source.
func NewArea() datasource.DataSource {
	return &areaDataSource{}
}

type areaDataSource struct {
	areas *client.Registry[client.Area]
}

func (d *areaDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_area"
}

func (d *areaDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := computedAreaAttributes()
	attrs["area_id"] = schema.StringAttribute{
		Description: "Area ID to look up. Set exactly one of `area_id` and `name`.",
		Optional:    true,
		Computed:    true,
	}
	attrs["name"] = schema.StringAttribute{
		Description: "Name to look up, compared exactly. Set exactly one of `area_id` and `name`.",
		Optional:    true,
		Computed:    true,
	}
	resp.Schema = schema.Schema{
		Description: "Reads one area (`config/area_registry/list`), by ID or by name. Exactly one " +
			"area must match.",
		Attributes: attrs,
	}
}

func (d *areaDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	resp.Diagnostics.Append(exactlyOneOf(ctx, req.Config, "area_id", "name")...)
}

func (d *areaDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if r := areaRegistryClient(req, resp); r != nil {
		d.areas = r
	}
}

func (d *areaDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	// ValidateConfig accepts unknown values. Here every value is known.
	resp.Diagnostics.Append(exactlyOneOf(ctx, req.Config, "area_id", "name")...)
	var cfg areaModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	areas, err := d.areas.List(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading areas", client.ErrorDetail(err))
		return
	}
	area, err := matchArea(areas, cfg.AreaID.ValueStringPointer(), cfg.Name.ValueStringPointer())
	if err != nil {
		resp.Diagnostics.AddError("Finding the area", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newAreaModel(area))...)
}

// matchArea returns the one area with the given ID or, if id is nil, the given name.
func matchArea(areas []client.Area, id, name *string) (client.Area, error) {
	var match func(client.Area) bool
	var what string
	if id != nil {
		match = func(a client.Area) bool { return a.AreaID == *id }
		what = fmt.Sprintf("ID %q", *id)
	} else {
		match = func(a client.Area) bool { return a.Name == *name }
		what = fmt.Sprintf("name %q", *name)
	}
	var matches []client.Area
	for _, a := range areas {
		if match(a) {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return client.Area{}, fmt.Errorf("no area has %s", what)
	}
	ids := make([]string, len(matches))
	for i, a := range matches {
		ids[i] = a.AreaID
	}
	return client.Area{}, fmt.Errorf("%d areas have %s: %s; look the area up by `area_id`",
		len(matches), what, strings.Join(ids, ", "))
}

// stringSet returns the strings as a set; nil becomes an empty set.
func stringSet(ss []string) types.Set {
	elems := make([]attr.Value, len(ss))
	for i, s := range ss {
		elems[i] = types.StringValue(s)
	}
	return types.SetValueMust(types.StringType, elems)
}
