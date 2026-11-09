# The primary entities of every device from one read: skip config and diagnostic entities, and
# hidden or disabled ones. A device often has several, e.g. a light with its power and energy
# sensors, so each device maps to a list.
data "homeassistant_entity_registry" "all" {}

locals {
  primary_entities = {
    for e in data.homeassistant_entity_registry.all.entities :
    e.device_id => e.entity_id... if e.device_id != null && e.entity_category == null
    && e.hidden_by == null && e.disabled_by == null
  }
}

output "primary_entities_by_device" {
  value = local.primary_entities
}

# The update entities of HACS, including disabled ones, e.g. to disable them all with
# homeassistant_entity_settings and find them again on the next plan.
data "homeassistant_entity_registry" "hacs_updates" {
  domain   = "update"
  platform = "hacs"
}

output "hacs_update_entities" {
  value = { for e in data.homeassistant_entity_registry.hacs_updates.entities : e.entity_id => e.disabled_by }
}
