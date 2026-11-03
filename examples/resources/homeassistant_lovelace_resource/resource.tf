# The JavaScript of a custom card, e.g. one installed with HACS, so dashboards can use it.
resource "homeassistant_lovelace_resource" "button_card" {
  url  = "/hacsfiles/button-card/button-card.js"
  type = "module"
}

resource "homeassistant_dashboard" "home" {
  url_path = "my-home"
  title    = "My Home"
  config = {
    views = [{
      type  = "sections"
      title = "Home"
      sections = [{
        type  = "grid"
        cards = [{ type = "custom:button-card", entity = "light.kitchen" }]
      }]
    }]
  }

  depends_on = [homeassistant_lovelace_resource.button_card]
}
