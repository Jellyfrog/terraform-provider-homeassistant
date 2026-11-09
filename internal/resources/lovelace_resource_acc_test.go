package resources_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/toelke/terraform-provider-homeassistant/internal/acctest"
	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

// lovelaceResources returns the Lovelace resources of the shared instance.
func lovelaceResources(t *testing.T) client.Registry[client.LovelaceResource] {
	t.Helper()
	return client.LovelaceResources(haClient(t).WS)
}

// lovelaceResourceWithURL returns the resource with the given URL, if there is one.
func lovelaceResourceWithURL(t *testing.T, url string) (client.LovelaceResource, bool) {
	t.Helper()
	r, ok, err := lovelaceResources(t).Find(context.Background(), func(r client.LovelaceResource) bool { return r.URL == url })
	if err != nil {
		t.Fatal(err)
	}
	return r, ok
}

func lovelaceResourceConfig(url, typ string) string {
	return acctest.ProviderConfig + fmt.Sprintf(`
resource "homeassistant_lovelace_resource" "test" {
  url  = %q
  type = %q
}
`, url, typ)
}

func TestAccLovelaceResource(t *testing.T) {
	const (
		addr    = "homeassistant_lovelace_resource.test"
		url     = "/local/acc-card.js"
		renamed = "/local/acc-card-v2.css"
	)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			for _, u := range []string{url, renamed} {
				if _, ok := lovelaceResourceWithURL(t, u); ok {
					t.Errorf("Lovelace resource %q still exists", u)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: lovelaceResourceConfig(url, "module"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(addr, "id"),
					resource.TestCheckResourceAttr(addr, "url", url),
					resource.TestCheckResourceAttr(addr, "type", "module"),
				),
			},
			{
				ResourceName:      addr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			importStep(addr, url), // by URL
			{
				// URL and type change in place.
				Config: lovelaceResourceConfig(renamed, "css"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "url", renamed),
					resource.TestCheckResourceAttr(addr, "type", "css"),
					func(*terraform.State) error {
						if r, ok := lovelaceResourceWithURL(t, renamed); !ok || r.Type != "css" {
							return fmt.Errorf("in Home Assistant: %+v, %v", r, ok)
						}
						return nil
					},
				),
			},
			{
				// Deleted outside Tofu: the resource drops out of state and is created again.
				PreConfig: func() {
					r, ok := lovelaceResourceWithURL(t, renamed)
					if !ok {
						t.Fatal("resource missing")
					}
					if err := lovelaceResources(t).Delete(context.Background(), r.ID); err != nil {
						t.Fatal(err)
					}
				},
				Config: lovelaceResourceConfig(renamed, "css"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestAccLovelaceResource_ExistingURL(t *testing.T) {
	const url = "/local/acc-existing.js"
	existing, err := lovelaceResources(t).Create(context.Background(), map[string]any{"res_type": "module", "url": url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lovelaceResources(t).Delete(context.Background(), existing.ID) })

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      lovelaceResourceConfig(url, "module"),
				ExpectError: regexp.MustCompile(`(?s)Lovelace resource exists.*/local/acc-existing.js`),
			},
		},
	})
}
