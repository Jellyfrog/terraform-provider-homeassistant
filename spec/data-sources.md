# Data sources (REST)

Read-only lookups of live HA data. They use REST only, so they never open a WebSocket.

## `homeassistant_config`

`GET /api/config`.

| Computed | Type |
|---|---|
| `version` | string |
| `location_name` | string |
| `latitude`, `longitude`, `elevation` | number |
| `time_zone` | string |
| `unit_system` | object `{ temperature, length, mass, volume }` |
| `components` | list(string), sorted (HA builds it from a set, so its order is not stable) |

## `homeassistant_entity`

`GET /api/states/<entity_id>`. A 404 is an error, with a hint to check the ID.

| Argument | Type | |
|---|---|---|
| `entity_id` | string | required; validated at plan time as `<domain>.<object_id>` (lowercase letters, digits, `_`) |

| Computed | Type | Source |
|---|---|---|
| `state` | string | |
| `friendly_name` | string | `attributes.friendly_name` |
| `device_class` | string | `attributes.device_class` |
| `unit_of_measurement` | string | `attributes.unit_of_measurement` |
| `attributes` | dynamic | all attributes, keeping JSON types (ADR-0015) |
| `last_changed`, `last_updated` | string | ISO 8601 |

`friendly_name`, `device_class`, and `unit_of_measurement` are null when the attribute is
missing or not a string.

## `homeassistant_entities`

```hcl
data "homeassistant_entities" "bedroom_lights" {
  domain = "light"
  area   = "bedroom"          # area id or name
}
```

Every filter is optional, and an entity must match all that are set.

| Argument | Type | Notes |
|---|---|---|
| `domain` | string | the part of the entity ID before the dot |
| `area` | string | area ID or name, resolved through `area_entities()`; an unknown area matches nothing |
| `label` | string | label ID or name, resolved through `label_entities()`; an unknown label matches nothing |
| `device_class` | string | equals `attributes.device_class` |
| `name_pattern` | string | glob against `friendly_name`: `*` is any characters, `?` one character, everything else is literal and case-sensitive. Entities without a `friendly_name` never match. Since HA 2026.11, `friendly_name` has the device name in front of user-set names too and follows device renames (see [registries.md](registries.md)), so the description points to `*` patterns and `device_id` |
| `hidden` | bool | `false`: only entities that aren't hidden; `true`: only hidden ones; unset: no filter. Hidden means `hidden_by` is set, by the user or an integration. Entities without a registry entry are never hidden (ADR-0022) |
| `device_id` | string | device ID, resolved through `device_entities()`; an unknown device matches nothing. To look up a device by name, use `data.homeassistant_device` (ADR-0022) |

There is no `state` filter (ADR-0015), and no `disabled` filter: HA removes disabled entities
from `/api/states` (ADR-0022).

| Computed | Type |
|---|---|
| `entity_ids` | list(string), sorted |
| `entities` | dynamic: an object keyed by entity ID, each value `{ state, friendly_name, attributes, last_changed, hidden }` |

`entities` is dynamic rather than `map(object)`, because terraform-plugin-framework does not
allow a dynamic value (`attributes`, ADR-0015) inside a map. HCL reads it the same way:
`entities["light.x"].state`, and `for` expressions work on it. `friendly_name` is null when the
entity has none.

**Algorithm:**

1. `GET /api/states`.
2. If `area`, `label`, or `device_id` is set, render `{{ area_entities("<x>") | tojson }}`,
   `{{ label_entities("<x>") | tojson }}`, or `{{ device_entities("<x>") | tojson }}` through the
   template API, JSON-decode the result, and intersect it with the states. The value is written as a Jinja string literal in which
   `"`, `\`, and every character outside printable ASCII become a `\uXXXX` or `\UXXXXXXXX`
   escape, so it can't end the literal.
3. Render `{{ states | map(attribute="entity_id") | select("is_hidden_entity") | list | tojson }}`
   once, which gives every entity's `hidden` flag, and apply the `hidden` filter if it is set.
4. Apply the remaining filters.
5. Sort the result by entity ID.

## `homeassistant_template`

`POST /api/template` with `{"template": "..."}`. It returns the rendered text in `result`. The
HTTP status is checked, and so is a 200 body starting with `Error rendering template`. Either
becomes an error diagnostic. HA answers a broken template with HTTP 400, so a 400 or such a 200
body is reported on `template`; any other failure is reported like every other REST error.

```hcl
data "homeassistant_template" "areas" {
  template = "{{ areas() | tojson }}"
}
locals {
  area_ids = jsondecode(data.homeassistant_template.areas.result)
}
```
