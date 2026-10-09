package provider

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/nitra/terraform-provider-ory/internal/testhydra"
)

// Acceptance tests run against the local Hydra from compose.yaml:
//
//	podman compose up -d --wait
//	make testacc        # TF_ACC=1, uses `tofu` when available
//	podman compose down -v
var testAccProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"hydra": providerserver.NewProtocol6WithError(New("test")()),
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	testhydra.Ready(t)
}

func providerBlock() string {
	return fmt.Sprintf("provider \"hydra\" {\n  endpoint = %q\n}\n", testhydra.AdminURL())
}

// checkNoValueInState fails if any attribute of any resource in state
// contains secret (write-only values must never be persisted).
func checkNoValueInState(secret string) func(*terraform.State) error {
	return func(s *terraform.State) error {
		for name, rs := range s.RootModule().Resources {
			for k, v := range rs.Primary.Attributes {
				if strings.Contains(v, secret) {
					return fmt.Errorf("secret found in state: %s.%s", name, k)
				}
			}
		}
		return nil
	}
}

func TestMain(m *testing.M) {
	// Prefer OpenTofu for acceptance tests when no CLI is pinned explicitly.
	if os.Getenv("TF_ACC") != "" && os.Getenv("TF_ACC_TERRAFORM_PATH") == "" {
		for _, p := range []string{"/opt/homebrew/bin/tofu", "/usr/local/bin/tofu", "/usr/bin/tofu"} {
			if _, err := os.Stat(p); err == nil {
				_ = os.Setenv("TF_ACC_TERRAFORM_PATH", p)
				_ = os.Setenv("TF_ACC_PROVIDER_HOST", "registry.opentofu.org")
				break
			}
		}
	}
	os.Exit(m.Run())
}
