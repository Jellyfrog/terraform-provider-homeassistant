package datasources

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

var _ datasource.DataSourceWithConfigure = (*entitiesDataSource)(nil)

// entitiesReader is the part of the client the entities data source uses.
type entitiesReader interface {
	States(ctx context.Context) ([]client.EntityState, error)
	RenderTemplate(ctx context.Context, template string) (string, error)
}

// NewEntities returns the `homeassistant_entities` data source.
func NewEntities() datasource.DataSource {
	return &entitiesDataSource{}
}

type entitiesDataSource struct {
	client entitiesReader
}

type entitiesModel struct {
	Domain      types.String  `tfsdk:"domain"`
	Area        types.String  `tfsdk:"area"`
	Label       types.String  `tfsdk:"label"`
	DeviceClass types.String  `tfsdk:"device_class"`
	NamePattern types.String  `tfsdk:"name_pattern"`
	Hidden      types.Bool    `tfsdk:"hidden"`
	DeviceID    types.String  `tfsdk:"device_id"`
	EntityIDs   types.List    `tfsdk:"entity_ids"`
	Entities    types.Dynamic `tfsdk:"entities"`
}

func (d *entitiesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_entities"
}

func (d *entitiesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the entities that match every filter that is set (`GET /api/states`). " +
			"Without filters, it lists every entity. There is no filter on the current state, so " +
			"the result only changes when entities or their registry entries change.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Description: "Only entities of this domain, e.g. `light`.",
				Optional:    true,
			},
			"area": schema.StringAttribute{
				Description: "Only entities in this area, given by area ID or name. Entities of a device " +
					"in the area count too. An unknown area matches no entities.",
				Optional: true,
			},
			"label": schema.StringAttribute{
				Description: "Only entities with this label, given by label ID or name. An unknown label " +
					"matches no entities.",
				Optional: true,
			},
			"device_class": schema.StringAttribute{
				Description: "Only entities whose `device_class` attribute is this, e.g. `temperature`.",
				Optional:    true,
			},
			"name_pattern": schema.StringAttribute{
				Description: "Only entities whose `friendly_name` matches this glob pattern: `*` matches " +
					"any characters, `?` matches one character, and everything else matches itself, " +
					"case-sensitively. Entities without a `friendly_name` never match. Since Home " +
					"Assistant 2026.11, `friendly_name` puts the device name in front of names set by " +
					"the user too, and it changes when the device is renamed, for every entity without " +
					"a name of its own. A leading `*` (`\"*Power\"`) matches with and without the " +
					"device name, and `?` covers a change of case (`\"C?2\"`). Home Assistant plans to " +
					"deprecate `friendly_name`; filtering by `device_id` is more stable.",
				Optional: true,
			},
			"device_id": schema.StringAttribute{
				Description: "Only entities of this device, given by device ID. An unknown device matches " +
					"no entities. To look up a device by name, use `data.homeassistant_device`.",
				Optional: true,
			},
			"hidden": schema.BoolAttribute{
				Description: "`false` gives only entities that aren't hidden, `true` only hidden ones; " +
					"unset applies no filter. An entity is hidden when its registry entry has " +
					"`hidden_by` set, by the user or by an integration. Entities without a registry " +
					"entry are never hidden. There is no filter on disabled entities: HA removes them " +
					"from the states.",
				Optional: true,
			},
			"entity_ids": schema.ListAttribute{
				Description: "IDs of the matching entities, sorted.",
				ElementType: types.StringType,
				Computed:    true,
			},
			"entities": schema.DynamicAttribute{
				Description: "The matching entities, as an object keyed by entity ID. Each value has " +
					"`state`, `friendly_name` (null if the entity has none), `attributes` (keeping " +
					"their JSON types), `last_changed`, and `hidden`.",
				Computed: true,
			},
		},
	}
}

func (d *entitiesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if c := restClient(req, resp); c != nil {
		d.client = c
	}
}

func (d *entitiesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg entitiesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	states, err := d.client.States(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading entity states", client.ErrorDetail(err))
		return
	}

	f := entityFilter{
		domain:      cfg.Domain.ValueString(),
		deviceClass: cfg.DeviceClass.ValueString(),
	}
	if !cfg.NamePattern.IsNull() {
		f.name = globRegexp(cfg.NamePattern.ValueString())
	}
	for _, m := range []struct {
		attribute string
		value     types.String
		function  string
		target    *map[string]bool
	}{
		{"area", cfg.Area, "area_entities", &f.area},
		{"label", cfg.Label, "label_entities", &f.label},
		{"device_id", cfg.DeviceID, "device_entities", &f.device},
	} {
		if m.value.IsNull() {
			continue
		}
		ids, err := d.entityIDsOf(ctx, m.function, m.value.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root(m.attribute),
				fmt.Sprintf("Resolving the entities of %s %q", m.attribute, m.value.ValueString()),
				client.ErrorDetail(err))
			return
		}
		*m.target = ids
	}

	f.hiddenIDs, err = d.hiddenEntityIDs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Finding the hidden entities", client.ErrorDetail(err))
		return
	}
	if !cfg.Hidden.IsNull() {
		hidden := cfg.Hidden.ValueBool()
		f.hidden = &hidden
	}

	matched := f.apply(states)
	ids := make([]attr.Value, len(matched))
	for i, s := range matched {
		ids[i] = types.StringValue(s.EntityID)
	}
	cfg.EntityIDs = types.ListValueMust(types.StringType, ids)
	cfg.Entities, err = entitiesValue(matched, f.hiddenIDs)
	if err != nil {
		resp.Diagnostics.AddError("Decoding entity attributes", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}

// entityIDsOf renders `<function>(<value>)`, e.g. area_entities, through the template API and
// returns the entity IDs as a set.
func (d *entitiesDataSource) entityIDsOf(ctx context.Context, function, value string) (map[string]bool, error) {
	return d.renderIDs(ctx, function, fmt.Sprintf("{{ %s(%s) | tojson }}", function, jinjaString(value)))
}

// hiddenEntityIDs returns the IDs of every hidden entity as a set, through is_hidden_entity.
func (d *entitiesDataSource) hiddenEntityIDs(ctx context.Context) (map[string]bool, error) {
	return d.renderIDs(ctx, "is_hidden_entity",
		`{{ states | map(attribute="entity_id") | select("is_hidden_entity") | list | tojson }}`)
}

// renderIDs renders template, which yields a JSON list of entity IDs, and returns them as a set.
// function names the template function in errors.
func (d *entitiesDataSource) renderIDs(ctx context.Context, function, template string) (map[string]bool, error) {
	result, err := d.client.RenderTemplate(ctx, template)
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(result), &ids); err != nil {
		return nil, fmt.Errorf("decoding the result of %s: %w", function, err)
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}

// entityFilter holds the filters of the entities data source. Zero values don't filter.
type entityFilter struct {
	domain      string
	deviceClass string
	name        *regexp.Regexp
	// area, label, and device are the entity IDs in the area, with the label, and of the device.
	area, label, device map[string]bool
	// hidden, if set, is whether the entity must be in hiddenIDs, the IDs of hidden entities.
	hidden    *bool
	hiddenIDs map[string]bool
}

// apply returns the states that pass every filter, sorted by entity ID.
func (f entityFilter) apply(states []client.EntityState) []client.EntityState {
	return filterSorted(states, f.matches, func(s client.EntityState) string { return s.EntityID })
}

func (f entityFilter) matches(s client.EntityState) bool {
	if f.domain != "" && !inDomain(s.EntityID, f.domain) {
		return false
	}
	if f.area != nil && !f.area[s.EntityID] {
		return false
	}
	if f.label != nil && !f.label[s.EntityID] {
		return false
	}
	if f.hidden != nil && f.hiddenIDs[s.EntityID] != *f.hidden {
		return false
	}
	if f.device != nil && !f.device[s.EntityID] {
		return false
	}
	if f.deviceClass == "" && f.name == nil {
		return true
	}
	var attrs struct {
		FriendlyName any `json:"friendly_name"`
		DeviceClass  any `json:"device_class"`
	}
	_ = json.Unmarshal(s.Attributes, &attrs) // missing or malformed attributes match nothing
	if f.deviceClass != "" && attrs.DeviceClass != f.deviceClass {
		return false
	}
	if f.name != nil {
		name, ok := attrs.FriendlyName.(string)
		if !ok || !f.name.MatchString(name) {
			return false
		}
	}
	return true
}

// globRegexp compiles a glob pattern: `*` matches any characters, `?` one character, and
// everything else itself.
func globRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`(?s)^`)
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(`.*`)
		case '?':
			b.WriteString(`.`)
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString(`$`)
	return regexp.MustCompile(b.String())
}

// jinjaString quotes s as a Jinja string literal. Jinja decodes the backslash escapes of a
// literal like Python's unicode-escape codec, so every character other than printable ASCII, and
// the quote and backslash, is written as `\uXXXX` or `\UXXXXXXXX`. Nothing in the result can
// end the literal or the surrounding `{{ }}`.
func jinjaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\' || r < 0x20 || r > 0x7e:
			if r > 0xffff {
				fmt.Fprintf(&b, `\U%08x`, r)
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// entitiesValue returns the `entities` attribute: an object keyed by entity ID. hidden holds the
// IDs of the hidden entities.
func entitiesValue(states []client.EntityState, hidden map[string]bool) (types.Dynamic, error) {
	typs := make(map[string]attr.Type, len(states))
	values := make(map[string]attr.Value, len(states))
	for _, s := range states {
		attrs, flat, err := decodeAttributes(&s)
		if err != nil {
			return types.Dynamic{}, fmt.Errorf("entity %s: %w", s.EntityID, err)
		}
		a := attrs.UnderlyingValue()
		attrTypes := map[string]attr.Type{
			"state":         types.StringType,
			"friendly_name": types.StringType,
			"attributes":    a.Type(context.Background()),
			"last_changed":  types.StringType,
			"hidden":        types.BoolType,
		}
		v, diags := types.ObjectValue(attrTypes, map[string]attr.Value{
			"state":         types.StringValue(s.State),
			"friendly_name": stringAttribute(flat, "friendly_name"),
			"attributes":    a,
			"last_changed":  types.StringValue(s.LastChanged),
			"hidden":        types.BoolValue(hidden[s.EntityID]),
		})
		if diags.HasError() {
			return types.Dynamic{}, fmt.Errorf("entity %s: %v", s.EntityID, diags)
		}
		typs[s.EntityID] = types.ObjectType{AttrTypes: attrTypes}
		values[s.EntityID] = v
	}
	obj, diags := types.ObjectValue(typs, values)
	if diags.HasError() {
		return types.Dynamic{}, fmt.Errorf("building entities: %v", diags)
	}
	return types.DynamicValue(obj), nil
}
