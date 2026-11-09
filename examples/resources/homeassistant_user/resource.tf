# A user who logs in with a username and password, and the person that tracks them.
resource "homeassistant_user" "jane" {
  name     = "Jane"
  username = "jane"
  password = var.jane_password
  group    = "user"
}

resource "homeassistant_person" "jane" {
  name            = "Jane"
  user_id         = homeassistant_user.jane.id
  device_trackers = ["device_tracker.jane_phone"]
}

# With OpenTofu or Terraform 1.11 or later, a write-only password stays out of the state. Bump
# password_wo_version to set a new one.
resource "homeassistant_user" "john" {
  name                = "John"
  username            = "john"
  password_wo         = var.john_password
  password_wo_version = 1
}

# A user without a password login, e.g. for a wall tablet that logs in with a token.
resource "homeassistant_user" "wall_tablet" {
  name       = "Wall tablet"
  group      = "read-only"
  local_only = true
}

variable "john_password" {
  type      = string
  sensitive = true
}

variable "jane_password" {
  type      = string
  sensitive = true
}
