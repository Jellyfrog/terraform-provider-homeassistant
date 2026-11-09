package resources

import (
	"context"
	"errors"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Helpers shared by the settings resources (ADR-0010).

// computedString is a read-only string attribute that keeps its value across plans.
func computedString(description string) schema.StringAttribute {
	return schema.StringAttribute{
		Description:   description,
		Computed:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// fieldChange adds the registry field key to fields if the attribute turns from from into to:
// value() if to is set and differs from from, reset if to is unset and from was set. An
// attribute unset in both is left out, so only configured fields are ever written.
func fieldChange(fields map[string]any, key string, to, from attr.Value, value func() any, reset any) {
	switch {
	case !to.IsNull() && !to.Equal(from):
		fields[key] = value()
	case to.IsNull() && !from.IsNull():
		fields[key] = reset
	}
}

// clearChange adds the registry field key, reset to null, to fields if the attribute that clears
// it (`use_integration_name`, `inherit_area`) is true in to but not in from (ADR-0030). from is
// false when the registry holds a value, so a value set elsewhere is cleared again. Removing the
// attribute writes nothing: null is the default anyway.
func clearChange(fields map[string]any, key string, to, from types.Bool) {
	if to.ValueBool() && !from.ValueBool() {
		fields[key] = nil
	}
}

// onlyTrue allows only `true` for an attribute where `false` would mean nothing the provider can
// write, such as `use_integration_name`: leaving it out is how it is not managed.
type onlyTrue struct{}

func (onlyTrue) Description(context.Context) string { return "must be true if set" }

func (v onlyTrue) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (onlyTrue) ValidateBool(_ context.Context, req validator.BoolRequest, resp *validator.BoolResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() || req.ConfigValue.ValueBool() {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Only true is allowed",
		"Set it to true, or leave it out to not manage this setting.")
}

// flagChange adds the registry field key (`hidden_by` or `disabled_by`) to fields if the flag
// turns from from into to. `true` means "set by the user", `false` means "not set by the user".
// So the provider writes `"user"` to set the flag, and clears it only where the user set it:
// a flag set by an integration, device, or config entry is never cleared. Both unset and `false`
// leave such a flag alone, so from must be the registry's value whenever to is set
// (registryFlag).
func flagChange(fields map[string]any, key string, to, from types.Bool) {
	switch {
	case to.ValueBool() && !from.ValueBool():
		fields[key] = "user"
	case !to.ValueBool() && from.ValueBool():
		fields[key] = nil
	}
}

// userFlag reads a `hidden_by` or `disabled_by` value as a flag: true only if the user set it.
func userFlag(by *string) bool {
	return by != nil && *by == "user"
}

// registryFlag returns from, or, if to is set but from is not, the flag as the registry has it.
// A flag that becomes managed has no value in from yet, but flagChange needs one to tell whether
// a `false` must clear a flag the user set.
func registryFlag(to, from types.Bool, by *string) types.Bool {
	if !to.IsNull() && from.IsNull() {
		return types.BoolValue(userFlag(by))
	}
	return from
}

// needsRegistryFlag reports whether registryFlag needs the registry's value for this flag:
// `false` newly configured. A newly configured `true` is written either way.
func needsRegistryFlag(to, from types.Bool) bool {
	return !to.IsNull() && !to.ValueBool() && from.IsNull()
}

// awaitRegistry polls get with backoff until the registry entry exists, for at most timeout. If
// it does not appear in time, the error is notFound.
func awaitRegistry[T any](ctx context.Context, timeout time.Duration, get func(context.Context) (T, bool, error), notFound error) (T, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var entry T
	err := waitFor(waitCtx, func(ctx context.Context) (bool, error) {
		var ok bool
		var err error
		entry, ok, err = get(ctx)
		return ok, err
	})
	if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return entry, notFound
	}
	return entry, err
}
