package resources_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/toelke/terraform-provider-homeassistant/internal/acctest"
	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

func users(t *testing.T) client.Users {
	t.Helper()
	return client.NewUsers(haClient(t).WS)
}

// userByName returns the user with the given display name, if there is one.
func userByName(t *testing.T, name string) (client.User, bool) {
	t.Helper()
	all, err := users(t).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(u client.User) bool { return u.Name == name })
	if i < 0 {
		return client.User{}, false
	}
	return all[i], true
}

// canLogIn reports whether Home Assistant accepts the username and password in its login flow.
func canLogIn(t *testing.T, username, password string) bool {
	t.Helper()
	base := acctest.SharedInstance(t).URL
	clientID := base + "/" // as onboarding uses
	post := func(path string, body any) map[string]any {
		b, _ := json.Marshal(body)
		resp, err := http.Post(base+path, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("POST %s: status %d: %v", path, resp.StatusCode, err)
		}
		return out
	}
	flow := post("/auth/login_flow", map[string]any{
		"client_id": clientID, "handler": []any{"homeassistant", nil}, "redirect_uri": clientID,
	})
	step := post(fmt.Sprintf("/auth/login_flow/%v", flow["flow_id"]), map[string]any{
		"client_id": clientID, "username": username, "password": password,
	})
	return step["type"] == "create_entry"
}

func checkLogIn(t *testing.T, username, password string, want bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := canLogIn(t, username, password); got != want {
			return fmt.Errorf("log in as %q: %v, want %v", username, got, want)
		}
		return nil
	}
}

func userConfig(body string) string {
	return acctest.ProviderConfig + fmt.Sprintf(`
resource "homeassistant_user" "test" {
%s
}
`, body)
}

func TestAccUser(t *testing.T) {
	const addr = "homeassistant_user.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			for _, name := range []string{"Acc User", "Acc User Renamed"} {
				if _, ok := userByName(t, name); ok {
					t.Errorf("user %q still exists", name)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: userConfig(`
  name     = "Acc User"
  username = "acc-user"
  password = "acc-password-1"
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(addr, "id", regexp.MustCompile(`^[0-9a-f]{32}$`)),
					resource.TestCheckResourceAttr(addr, "name", "Acc User"),
					resource.TestCheckResourceAttr(addr, "username", "acc-user"),
					resource.TestCheckResourceAttr(addr, "group", "user"),
					resource.TestCheckResourceAttr(addr, "is_active", "true"),
					resource.TestCheckResourceAttr(addr, "local_only", "false"),
					func(*terraform.State) error {
						u, ok := userByName(t, "Acc User")
						if !ok || u.Username == nil || *u.Username != "acc-user" || !slices.Equal(u.GroupIDs, []string{client.GroupUser}) {
							return fmt.Errorf("in Home Assistant: %+v, %v", u, ok)
						}
						return nil
					},
					checkLogIn(t, "acc-user", "acc-password-1", true),
				),
			},
			{
				ResourceName:            addr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password"},
			},
			{
				// By username.
				ResourceName:            addr,
				ImportState:             true,
				ImportStateId:           "acc-user",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password"},
			},
			{
				// Everything changes in place; username and password need the owner's token, which
				// the tests have.
				Config: userConfig(`
  name     = "Acc User Renamed"
  username = "acc-user-2"
  password = "acc-password-2"
  group    = "admin"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", "Acc User Renamed"),
					resource.TestCheckResourceAttr(addr, "username", "acc-user-2"),
					resource.TestCheckResourceAttr(addr, "group", "admin"),
					checkLogIn(t, "acc-user-2", "acc-password-2", true),
					checkLogIn(t, "acc-user-2", "acc-password-1", false),
				),
			},
			{
				// Without username and password, the login is deleted.
				Config: userConfig(`
  name       = "Acc User Renamed"
  group      = "read-only"
  is_active  = false
  local_only = true
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(addr, "username"),
					resource.TestCheckResourceAttr(addr, "group", "read-only"),
					resource.TestCheckResourceAttr(addr, "is_active", "false"),
					resource.TestCheckResourceAttr(addr, "local_only", "true"),
					func(*terraform.State) error {
						u, ok := userByName(t, "Acc User Renamed")
						if !ok || u.Username != nil || u.IsActive || !u.LocalOnly ||
							!slices.Equal(u.GroupIDs, []string{client.GroupReadOnly}) {
							return fmt.Errorf("in Home Assistant: %+v, %v", u, ok)
						}
						return nil
					},
				),
			},
			{
				// Deleted outside OpenTofu: the user drops out of state and is created again.
				PreConfig: func() {
					u, ok := userByName(t, "Acc User Renamed")
					if !ok {
						t.Fatal("user missing")
					}
					if err := users(t).Delete(context.Background(), u.ID); err != nil {
						t.Fatal(err)
					}
				},
				Config: userConfig(`
  name       = "Acc User Renamed"
  group      = "read-only"
  is_active  = false
  local_only = true
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestAccUser_ImportOwnerFails(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        userConfig(`  name = "Acceptance Test"`),
				ResourceName:  "homeassistant_user.test",
				ImportState:   true,
				ImportStateId: "acctest", // the owner that onboarding creates
				ExpectError:   regexp.MustCompile(`User is the owner`),
			},
		},
	})
}

func TestAccUser_WriteOnlyPassword(t *testing.T) {
	const addr = "homeassistant_user.test"
	config := func(password string, version int) string {
		return userConfig(fmt.Sprintf(`
  name                = "Acc WO User"
  username            = "acc-wo-user"
  password_wo         = %q
  password_wo_version = %d
`, password, version))
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		// Write-only attributes need OpenTofu or Terraform 1.11.
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		CheckDestroy: func(*terraform.State) error {
			if _, ok := userByName(t, "Acc WO User"); ok {
				t.Error(`user "Acc WO User" still exists`)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("acc-wo-password-1", 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(addr, "password"),
					resource.TestCheckNoResourceAttr(addr, "password_wo"),
					resource.TestCheckResourceAttr(addr, "password_wo_version", "1"),
					checkLogIn(t, "acc-wo-user", "acc-wo-password-1", true),
				),
			},
			{
				// A new password with the same version is not applied: the provider cannot see it.
				Config: config("acc-wo-password-2", 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: checkLogIn(t, "acc-wo-user", "acc-wo-password-1", true),
			},
			{
				// A new version sets it.
				Config: config("acc-wo-password-2", 2),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "password_wo_version", "2"),
					checkLogIn(t, "acc-wo-user", "acc-wo-password-2", true),
					checkLogIn(t, "acc-wo-user", "acc-wo-password-1", false),
				),
			},
			{
				ResourceName:            addr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password_wo_version"},
			},
		},
	})
}
