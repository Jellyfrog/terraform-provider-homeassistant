package datasources

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestExactlyOneOfDefersUnknown checks that an unknown value does not count as set: it can still
// become null. Only the known values can show a conflict at validation.
func TestExactlyOneOfDefersUnknown(t *testing.T) {
	ctx := t.Context()
	var s datasource.SchemaResponse
	(&deviceDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &s)
	typ := s.Schema.Type().TerraformType(ctx).(tftypes.Object)
	list := tftypes.List{ElementType: tftypes.String}
	name := tftypes.NewValue(tftypes.String, "Plug")
	id := tftypes.NewValue(tftypes.String, "d1")
	unknownName := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	unknownIdentifier := tftypes.NewValue(list, tftypes.UnknownValue)

	for _, tc := range []struct {
		what    string
		attrs   map[string]tftypes.Value
		wantErr bool
	}{
		{"name and an unknown identifier", map[string]tftypes.Value{"name": name, "identifier": unknownIdentifier}, false},
		{"only an unknown identifier", map[string]tftypes.Value{"identifier": unknownIdentifier}, false},
		{"two unknown values", map[string]tftypes.Value{"name": unknownName, "identifier": unknownIdentifier}, false},
		{"one known value", map[string]tftypes.Value{"name": name}, false},
		{"two known values", map[string]tftypes.Value{"name": name, "device_id": id}, true},
		{"two known values and an unknown value", map[string]tftypes.Value{"name": name, "device_id": id, "identifier": unknownIdentifier}, true},
		{"no value", nil, true},
	} {
		vals := map[string]tftypes.Value{}
		for n, at := range typ.AttributeTypes {
			vals[n] = tftypes.NewValue(at, nil)
		}
		for n, v := range tc.attrs {
			vals[n] = v
		}
		config := tfsdk.Config{Schema: s.Schema, Raw: tftypes.NewValue(typ, vals)}
		diags := exactlyOneOf(ctx, config, "device_id", "name", "identifier")
		if diags.HasError() != tc.wantErr {
			t.Errorf("%s: error = %v, want %v (%v)", tc.what, diags.HasError(), tc.wantErr, diags)
		}
	}
}
