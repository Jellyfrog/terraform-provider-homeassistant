package datasources

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// registryWS is a WSCommander that answers the command typ with json, or with err if it is set.
type registryWS struct {
	typ, json string
	err       error
}

// Command answers ws.typ with ws.json decoded into result, or with ws.err.
func (ws registryWS) Command(_ context.Context, typ string, _ map[string]any, result any) error {
	if typ != ws.typ {
		return fmt.Errorf("unexpected command %q", typ)
	}
	if ws.err != nil {
		return ws.err
	}
	return json.Unmarshal([]byte(ws.json), result)
}

// readDataSource runs ds's Read with the config attrs (the others null) and returns the state.
func readDataSource(t *testing.T, ds datasource.DataSource, attrs map[string]tftypes.Value) tfsdk.State {
	t.Helper()
	resp := runRead(t, ds, attrs)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	return resp.State
}

// runRead runs ds's Read with the config attrs (the others null) and returns the response.
func runRead(t *testing.T, ds datasource.DataSource, attrs map[string]tftypes.Value) datasource.ReadResponse {
	t.Helper()
	ctx := t.Context()
	var s datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &s)
	typ := s.Schema.Type().TerraformType(ctx).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, at := range typ.AttributeTypes {
		vals[name] = tftypes.NewValue(at, nil)
	}
	for name, v := range attrs {
		vals[name] = v
	}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: s.Schema, Raw: tftypes.NewValue(typ, nil)}}
	ds.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s.Schema, Raw: tftypes.NewValue(typ, vals)}}, &resp)
	return resp
}

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
