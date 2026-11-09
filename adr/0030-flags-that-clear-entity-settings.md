# 30. `use_integration_name` and `inherit_area` clear an entity's name and area

Date: 2026-11-09 · Status: Accepted · Extends ADR-0010

## Context

`homeassistant_entity_settings` manages only the attributes that are set (ADR-0010). An unset
`name` or `area_id` is not managed, and Terraform cannot tell `name = null` from leaving `name`
out. So the resource can clear a name or area only if it managed one before: removing `name` from
the configuration resets it, but a name set elsewhere never shows as drift and is never cleared.

Since Home Assistant 2026.11, an entity without a name of its own shows its device's name, and HA
treats it as the device's main entity. Moving to that model means clearing many names set before
2026.11; otherwise the device name shows twice ("Haustür Haustür"). A real configuration needed 49
such clears, done with a separate WebSocket script after `tofu apply` (issue #97). The 2026.11
upgrade itself stores `""`, not `null`, when it strips a name equal to the device name (#98).
`area_id = null`, "inherit the device's area", has the same gap.

The options put to the maintainer on issue #97:

1. A flag per attribute, e.g. `use_integration_name = true` and `inherit_area = true`: the
   provider writes `null` and treats a non-null read-back as drift.
2. A sentinel value, e.g. `name = ""` for "no name of its own". Compact, but HA stores `""` itself
   in some cases, so `""` and `null` would have to read back as the same thing, and the meaning is
   easy to miss.
3. One attribute listing the settings to force to their default, e.g. `reset = ["name"]`. It can
   conflict with setting the same attribute.

The maintainer chose option 1.

## Decision

`homeassistant_entity_settings` gets two optional bool attributes:

- `use_integration_name`: the entity has no name of its own (`name` is `null` in the registry).
- `inherit_area`: the entity has no area of its own (`area_id` is `null`).

Each accepts only `true`; leaving it out leaves the setting unmanaged, as for every other
attribute. Each conflicts with the attribute it clears (`name`, `area_id`).

- Create and Update write `null` when the flag is `true` in the plan and not `true` in state.
- Read sets a managed flag to whether the registry value is `null`. Any value, `""` included,
  reads as `false` and plans an update that writes `null` again.
- Removing a flag, or destroying the resource, writes nothing: `null` is already the default.

## Consequences

- A configuration can enforce "no name of its own" or "inherit the device's area" without a
  script, and the 2026.11 migration's `""` names are cleared by the next apply.
- Two more attributes, and `false` is rejected at plan time instead of meaning something.
- `icon` has the same gap. It can get a flag the same way when someone needs it.
