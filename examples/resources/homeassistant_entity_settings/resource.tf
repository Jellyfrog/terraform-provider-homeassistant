resource "homeassistant_area" "bedroom" {
  name = "Bedroom"
}

resource "homeassistant_label" "managed" {
  name = "Managed"
}

# Only these attributes are managed. The icon, for example, can still be changed in the UI.
resource "homeassistant_entity_settings" "ceiling" {
  entity_id = "light.bedroom_ceiling"
  name      = "Ceiling Light"
  area_id   = homeassistant_area.bedroom.id
  labels    = [homeassistant_label.managed.id]
}

# Hide a sensor nobody needs to see. Give a slow integration more time to create it.
resource "homeassistant_entity_settings" "signal" {
  entity_id = "sensor.bedroom_ceiling_signal_strength"
  hidden    = true

  timeouts = {
    create = "5m"
  }
}

# The resource follows the entity's registry ID. Changing entity_id renames the entity in place;
# a rename made in the UI shows up as a change of entity_id until the config matches it. To manage
# a different entity instead, run `tofu apply -replace=homeassistant_entity_settings.<name>`.
#
# A resource in for_each keyed by entity ID changes its address when it is renamed. A moved block
# keeps it, so the rename happens in place instead of a destroy and a create.
resource "homeassistant_entity_settings" "lights" {
  for_each  = toset(["light.desk", "light.shelf"]) # was "light.office"
  entity_id = each.key
  area_id   = homeassistant_area.bedroom.id
}

moved {
  from = homeassistant_entity_settings.lights["light.office"]
  to   = homeassistant_entity_settings.lights["light.desk"]
}
