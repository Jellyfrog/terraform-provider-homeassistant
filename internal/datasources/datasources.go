// Package datasources holds the provider's data sources.
package datasources

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"

	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

var entityIDPath = path.Root("entity_id")

// restClient returns the REST client from the provider data, or nil before the provider is
// configured or on error.
func restClient(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *client.RESTClient {
	if c := haClient(req, resp); c != nil {
		return c.REST
	}
	return nil
}

// haClient returns the client from the provider data, or nil before the provider is configured
// or on error.
func haClient(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *client.HAClient {
	if req.ProviderData == nil {
		return nil
	}
	c, ok := req.ProviderData.(*client.HAClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *client.HAClient, got %T. This is a bug in the provider.", req.ProviderData))
		return nil
	}
	return c
}

// exactlyOneOf checks that exactly one of the named attributes is set. An unknown value can still
// become null, so it rejects only what the known values already decide: two or more known values,
// or no value that is known or unknown. Read calls it again, when every value is known.
func exactlyOneOf(ctx context.Context, config tfsdk.Config, names ...string) diag.Diagnostics {
	var diags diag.Diagnostics
	known, unknown := 0, 0
	for _, name := range names {
		var v attr.Value
		diags.Append(config.GetAttribute(ctx, path.Root(name), &v)...)
		switch {
		case v == nil || v.IsNull():
		case v.IsUnknown():
			unknown++
		default:
			known++
		}
	}
	if !diags.HasError() && (known > 1 || known+unknown == 0) {
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = "`" + n + "`"
		}
		list := strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
		diags.AddAttributeError(path.Root(names[0]), "Invalid attribute combination",
			fmt.Sprintf("Set exactly one of %s.", list))
	}
	return diags
}
