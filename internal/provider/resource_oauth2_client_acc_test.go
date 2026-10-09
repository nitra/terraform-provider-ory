package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/nitra/terraform-provider-ory/internal/testhydra"
)

func accName(prefix string) string { return fmt.Sprintf("tfacc-%s-%d", prefix, time.Now().UnixNano()) }

func checkClientAPI(id string, fn func(map[string]any) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		st, b := testhydra.Do(nopTB{}, http.MethodGet, testhydra.AdminURL()+"/admin/clients/"+id, nil)
		if st != http.StatusOK {
			return fmt.Errorf("GET client %s: %d %s", id, st, b)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		return fn(m)
	}
}

func checkToken(name string, ok bool, fn func() testhydra.TokenResult) resource.TestCheckFunc {
	return func(*terraform.State) error {
		r := fn()
		if r.OK() != ok {
			return fmt.Errorf("%s: expected ok=%v, got %s", name, ok, r)
		}
		return nil
	}
}

func TestAccOAuth2Client_public(t *testing.T) {
	id := accName("public")
	cfg := func(name, ccLifespan string) string {
		return providerBlock() + fmt.Sprintf(`
resource "hydra_oauth2_client" "test" {
  client_id                  = %q
  client_name                = %q
  token_endpoint_auth_method = "none"
  grant_types = [
    "urn:ietf:params:oauth:grant-type:jwt-bearer",
    "urn:ietf:params:oauth:grant-type:device_code",
    "refresh_token",
  ]
  response_types = ["code"]
  scope          = "openid apicurio:developer"
  audience       = ["https://apicurio.example"]
  redirect_uris  = ["https://app.example/callback"]
  skip_consent   = true
  metadata       = jsonencode({ team = "platform", tags = ["a", "b"] })

  jwt_bearer_grant_access_token_lifespan              = %q
  device_authorization_grant_access_token_lifespan    = "1h"
  device_authorization_grant_refresh_token_lifespan   = "720h"
}
`, id, name, ccLifespan)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: cfg("first", "10m"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hydra_oauth2_client.test", "id", id),
					resource.TestCheckResourceAttr("hydra_oauth2_client.test", "token_endpoint_auth_method", "none"),
					resource.TestCheckResourceAttr("hydra_oauth2_client.test", "grant_types.#", "3"),
					resource.TestCheckResourceAttr("hydra_oauth2_client.test", "jwt_bearer_grant_access_token_lifespan", "10m"),
					resource.TestCheckResourceAttr("hydra_oauth2_client.test", "subject_type", "public"),
					resource.TestCheckResourceAttr("hydra_oauth2_client.test", "skip_consent", "true"),
					resource.TestCheckNoResourceAttr("hydra_oauth2_client.test", "client_credentials_grant_access_token_lifespan"),
					checkClientAPI(id, func(m map[string]any) error {
						if m["jwt_bearer_grant_access_token_lifespan"] != "10m0s" {
							return fmt.Errorf("lifespan in Hydra: %v", m["jwt_bearer_grant_access_token_lifespan"])
						}
						if m["device_authorization_grant_refresh_token_lifespan"] != "720h0m0s" {
							return fmt.Errorf("device lifespan in Hydra: %v", m["device_authorization_grant_refresh_token_lifespan"])
						}
						return nil
					}),
				),
			},
			// semantic equality: 10m0s == 10m -> no diff
			{
				Config:   cfg("first", "10m0s"),
				PlanOnly: true,
			},
			{
				ResourceName:      "hydra_oauth2_client.test",
				ImportState:       true,
				ImportStateId:     id,
				ImportStateVerify: true,
				// Hydra returns canonical durations ("10m0s"); the next step
				// proves they are semantically equal to the config ("10m").
				ImportStateVerifyIgnore: []string{
					"jwt_bearer_grant_access_token_lifespan",
					"device_authorization_grant_access_token_lifespan",
					"device_authorization_grant_refresh_token_lifespan",
				},
			},
			// after import: config "10m" vs imported "10m0s" must not diff
			{
				Config: cfg("first", "10m"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: cfg("second", "15m"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("hydra_oauth2_client.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hydra_oauth2_client.test", "client_name", "second"),
					checkClientAPI(id, func(m map[string]any) error {
						if m["jwt_bearer_grant_access_token_lifespan"] != "15m0s" || m["client_name"] != "second" {
							return fmt.Errorf("not updated: %v / %v", m["client_name"], m["jwt_bearer_grant_access_token_lifespan"])
						}
						return nil
					}),
				),
			},
			// removing lifespans from config resets them in Hydra
			{
				Config: regexp.MustCompile(`(?m)^\s*device_authorization_grant_.*$`).ReplaceAllString(cfg("second", "15m"), ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("hydra_oauth2_client.test", "device_authorization_grant_access_token_lifespan"),
					checkClientAPI(id, func(m map[string]any) error {
						if m["device_authorization_grant_access_token_lifespan"] != nil || m["device_authorization_grant_refresh_token_lifespan"] != nil {
							return fmt.Errorf("lifespans not reset: %v", m)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccOAuth2Client_confidentialWriteOnlySecret(t *testing.T) {
	id := accName("conf")
	const s1, s2, s3 = "wo-secret-one-123456", "wo-secret-two-123456", "wo-secret-three-12345"
	cfg := func(secret string, version int, name, ccLifespan string) string {
		return providerBlock() + fmt.Sprintf(`
resource "hydra_oauth2_client" "test" {
  client_id                = %q
  client_name              = %q
  client_secret_wo         = %q
  client_secret_wo_version = %d
  grant_types              = ["client_credentials"]
  scope                    = "svc:read"
  client_credentials_grant_access_token_lifespan = %q
}
`, id, name, secret, version, ccLifespan)
	}
	cc := func(secret string) func() testhydra.TokenResult {
		return func() testhydra.TokenResult { return testhydra.ClientCredentials(nopTB{}, id, secret, "svc:read") }
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: cfg(s1, 1, "conf", "7m"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hydra_oauth2_client.test", "token_endpoint_auth_method", "client_secret_basic"),
					resource.TestCheckNoResourceAttr("hydra_oauth2_client.test", "client_secret_wo"),
					checkNoValueInState(s1),
					checkToken("secret v1", true, cc(s1)),
					func(*terraform.State) error {
						r := cc(s1)()
						if r.ExpiresIn < 400 || r.ExpiresIn > 420 {
							return fmt.Errorf("lifespan 7m not effective: %s", r)
						}
						return nil
					},
				),
			},
			// version bump -> PUT with the new secret
			{
				Config: cfg(s2, 2, "conf", "7m"),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkNoValueInState(s2),
					checkToken("old secret after rotation", false, cc(s1)),
					checkToken("secret v2", true, cc(s2)),
				),
			},
			// secret value changes but version does not -> nothing is sent;
			// other changes PUT without client_secret, which keeps the hash.
			{
				Config: cfg(s3, 2, "renamed", "8m"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hydra_oauth2_client.test", "client_name", "renamed"),
					checkNoValueInState(s3),
					checkToken("v2 still valid", true, cc(s2)),
					checkToken("unversioned s3 not applied", false, cc(s3)),
				),
			},
			{
				ResourceName:            "hydra_oauth2_client.test",
				ImportState:             true,
				ImportStateId:           id,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"client_secret_wo", "client_secret_wo_version", "client_credentials_grant_access_token_lifespan"},
			},
		},
	})
}

func TestAccOAuth2Client_confidentialRequiresSecret(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: providerBlock() + fmt.Sprintf(`
resource "hydra_oauth2_client" "test" {
  client_id   = %q
  grant_types = ["client_credentials"]
}
`, accName("nosecret")),
			ExpectError: regexp.MustCompile(`client_secret_wo is required`),
		}},
	})
}

func TestAccOAuth2Client_driftRecreate(t *testing.T) {
	id := accName("drift")
	cfg := providerBlock() + fmt.Sprintf(`
resource "hydra_oauth2_client" "test" {
  client_id                  = %q
  token_endpoint_auth_method = "none"
  grant_types                = ["urn:ietf:params:oauth:grant-type:jwt-bearer"]
}
`, id)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					st, b := testhydra.Admin(t, http.MethodDelete, "/admin/clients/"+id, nil)
					if st != http.StatusNoContent {
						t.Fatalf("manual delete: %d %s", st, b)
					}
				},
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("hydra_oauth2_client.test", plancheck.ResourceActionCreate)},
				},
				Check: checkClientAPI(id, func(map[string]any) error { return nil }),
			},
		},
	})
}

// nopTB adapts testhydra helpers for use inside TestCheckFuncs.
type nopTB struct{ testing.TB }

func (nopTB) Helper()                   {}
func (nopTB) Fatal(args ...any)         { panic(fmt.Sprint(args...)) }
func (nopTB) Fatalf(f string, a ...any) { panic(fmt.Sprintf(f, a...)) }
