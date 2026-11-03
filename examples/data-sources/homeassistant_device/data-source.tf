# Look a device up by the name shown in the UI. A name the user gave wins over the
# integration's name.
data "homeassistant_device" "desk_lamp" {
  name = "Desk Lamp"
}

output "desk_lamp_model" {
  value = data.homeassistant_device.desk_lamp.model
}

# Look a device up by one of its identifiers: unlike the name, it stays the same when the
# device is renamed, e.g. by the integration that discovered it.
data "homeassistant_device" "door_sensor" {
  identifier = ["mqtt", "zigbee2mqtt_0x00158d0004aaf230"]
}
