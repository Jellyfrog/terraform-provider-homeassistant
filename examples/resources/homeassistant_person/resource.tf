resource "homeassistant_user" "jane" {
  name     = "Jane"
  username = "jane"
  password = var.jane_password
}

# Jane is home when her phone or her laptop is.
resource "homeassistant_person" "jane" {
  name            = "Jane"
  user_id         = homeassistant_user.jane.id
  device_trackers = ["device_tracker.jane_phone", "device_tracker.jane_laptop"]
}

# A person without a user, e.g. a child without a login.
resource "homeassistant_person" "kid" {
  id              = "kid"
  name            = "Alex"
  device_trackers = ["device_tracker.alex_watch"]
}

variable "jane_password" {
  type      = string
  sensitive = true
}
