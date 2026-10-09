package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/nitra/terraform-provider-ory/internal/testhydra"
)

func TestAccTrustedJWTGrantIssuer(t *testing.T) {
	iss := "https://" + accName("iss") + ".example"
	sub := "repo:7n-45/rules-132:ref:refs/heads/main"
	k1 := testhydra.NewKey(t, "kid-one")
	k2 := testhydra.NewKey(t, "kid-two")
	clientID := accName("jwtpub")
	// fixed expiry 10 years ahead; second spelling of the same instant (+03:00)
	exp := time.Now().UTC().Add(10 * 365 * 24 * time.Hour).Truncate(time.Second)
	expUTC := exp.Format(time.RFC3339)
	expKyiv := exp.In(time.FixedZone("EEST", 3*3600)).Format(time.RFC3339)

	cfg := func(jwk, expiresAt string) string {
		return providerBlock() + fmt.Sprintf(`
resource "hydra_oauth2_client" "pub" {
  client_id                  = %q
  token_endpoint_auth_method = "none"
  grant_types                = ["urn:ietf:params:oauth:grant-type:jwt-bearer"]
  scope                      = "apicurio:developer"
}

resource "hydra_trusted_jwt_grant_issuer" "test" {
  issuer     = %q
  subject    = %q
  scope      = ["apicurio:developer"]
  jwk        = %q
  expires_at = %q
}

data "hydra_trusted_jwt_grant_issuers" "all" {
  issuer     = hydra_trusted_jwt_grant_issuer.test.issuer
  depends_on = [hydra_trusted_jwt_grant_issuer.test]
}
`, clientID, iss, sub, jwk, expiresAt)
	}
	jwt := func(k *testhydra.Key) func() testhydra.TokenResult {
		return func() testhydra.TokenResult {
			return testhydra.JWTBearer(nopTB{}, clientID, "", k.Assertion(nopTB{}, iss, sub, 30*time.Minute, true), "apicurio:developer")
		}
	}
	var firstID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: cfg(k1.PublicJWK(t), expUTC),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hydra_trusted_jwt_grant_issuer.test", "kid", "kid-one"),
					resource.TestCheckResourceAttr("hydra_trusted_jwt_grant_issuer.test", "allow_any_subject", "false"),
					resource.TestCheckResourceAttrSet("hydra_trusted_jwt_grant_issuer.test", "created_at"),
					resource.TestCheckResourceAttr("data.hydra_trusted_jwt_grant_issuers.all", "issuers.#", "1"),
					resource.TestCheckResourceAttr("data.hydra_trusted_jwt_grant_issuers.all", "issuers.0.kid", "kid-one"),
					func(s *terraform.State) error {
						firstID = s.RootModule().Resources["hydra_trusted_jwt_grant_issuer.test"].Primary.ID
						return nil
					},
					checkToken("public client jwt-bearer with kid-one", true, jwt(k1)),
				),
			},
			// same instant, other zone -> no diff
			{
				Config: cfg(k1.PublicJWK(t), expKyiv),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:            "hydra_trusted_jwt_grant_issuer.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"jwk"},
			},
			// after import (jwk null in state) the same key must not replace
			{
				Config: cfg(k1.PublicJWK(t), expKyiv),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("hydra_trusted_jwt_grant_issuer.test", plancheck.ResourceActionNoop)},
				},
			},
			// re-formatted JWK, same key -> no replacement
			{
				Config: cfg(strings.Replace(k1.PublicJWK(t), `{`, `{ `, 1), expUTC),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("hydra_trusted_jwt_grant_issuer.test", plancheck.ResourceActionUpdate)},
				},
			},
			// new kid -> replace
			{
				Config: cfg(k2.PublicJWK(t), expUTC),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("hydra_trusted_jwt_grant_issuer.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hydra_trusted_jwt_grant_issuer.test", "kid", "kid-two"),
					func(s *terraform.State) error {
						if id := s.RootModule().Resources["hydra_trusted_jwt_grant_issuer.test"].Primary.ID; id == firstID {
							return fmt.Errorf("trust was not replaced (id %s)", id)
						}
						if st, _ := testhydra.Do(nopTB{}, http.MethodGet, testhydra.AdminURL()+"/admin/trust/grants/jwt-bearer/issuers/"+firstID, nil); st != http.StatusNotFound {
							return fmt.Errorf("old trust still exists: %d", st)
						}
						return nil
					},
					checkToken("kid-two works", true, jwt(k2)),
					checkToken("kid-one revoked", false, jwt(k1)),
				),
			},
		},
	})
}

func TestAccTrustedJWTGrantIssuer_validation(t *testing.T) {
	k := testhydra.NewKey(t, "v")
	base := func(extra string) string {
		return providerBlock() + fmt.Sprintf(`
resource "hydra_trusted_jwt_grant_issuer" "test" {
  issuer = "https://v.example"
  scope  = ["a"]
  %s
}
`, extra)
	}
	priv := `{"kty":"RSA","kid":"x","n":"AQAB","e":"AQAB","d":"AQAB"}`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config:      base(fmt.Sprintf(`subject = "s"`+"\n"+`jwk = %q`+"\n"+`expires_at = "2001-01-01T00:00:00Z"`, k.PublicJWK(t))),
				ExpectError: regexp.MustCompile(`not in the future`),
			},
			{
				Config:      base(fmt.Sprintf(`subject = "s"`+"\n"+`jwk = %q`+"\n"+`expires_at = "2099-01-01T00:00:00Z"`, priv)),
				ExpectError: regexp.MustCompile(`private/symmetric key member "d"`),
			},
			{
				Config:      base(fmt.Sprintf(`subject = "s"`+"\n"+`allow_any_subject = true`+"\n"+`jwk = %q`+"\n"+`expires_at = "2099-01-01T00:00:00Z"`, k.PublicJWK(t))),
				ExpectError: regexp.MustCompile(`cannot be used together`),
			},
			{
				Config:      base(fmt.Sprintf(`subject = "s"`+"\n"+`jwk = %q`, k.PublicJWK(t))),
				ExpectError: regexp.MustCompile(`expires_at`),
			},
		},
	})
}
