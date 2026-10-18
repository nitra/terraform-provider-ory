package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nitra/terraform-provider-ory/internal/kratosadmin"
)

// Actual init/plan/show only, source-built provider, synthetic state and GET-only API.
// IPv4-mapped loopback exercises the remote policy code without external DNS/network.
func TestAccPlatformIdentityRemoteEndpointPlan(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC=1 requires local OpenTofu; no production resources")
	}
	tofu, err := exec.LookPath("tofu")
	if err != nil {
		t.Skip("OpenTofu unavailable")
	}
	var reads, writes, redirected atomic.Int64
	var redirect atomic.Bool
	doc := map[string]any{"schema_id": "default", "state": "active", "traits": map[string]any{"name": "Synthetic"}, "metadata_public": nil, "metadata_admin": nil, "verifiable_addresses": []any{}, "recovery_addresses": []any{}, "external_id": nil, "organization_id": nil}
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			w.WriteHeader(405)
			return
		}
		reads.Add(1)
		if r.URL.Path != "/admin/identities/"+platformID || r.URL.RawQuery != "" {
			w.WriteHeader(404)
			return
		}
		if redirect.Load() {
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		identity := map[string]any{"id": platformID, "updated_at": "2026-10-18T00:00:00Z", "credentials": map[string]any{"password": "sample-secret"}}
		for key, value := range doc {
			identity[key] = value
		}
		_ = json.NewEncoder(w).Encode(identity)
	}))
	defer server.Close()
	remote := strings.Replace(server.URL, "127.0.0.1", "[::ffff:127.0.0.1]", 1)
	if remote == server.URL {
		t.Fatal("fixture must use IPv4 loopback")
	}
	client, err := kratosadmin.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := client.Read(context.Background(), platformID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mirror := filepath.Join(root, "mirror")
	plugin := filepath.Join(mirror, "registry.opentofu.org", "nitra", "ory", "99.0.0", runtime.GOOS+"_"+runtime.GOARCH, "terraform-provider-ory_v99.0.0")
	if err := os.MkdirAll(filepath.Dir(plugin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("go", "build", "-o", plugin, "../..").Run(); err != nil {
		t.Fatalf("test provider build failed: %v", err)
	}
	cli := filepath.Join(root, "tofu.rc")
	if err := os.WriteFile(cli, []byte(fmt.Sprintf("disable_checkpoint = true\nprovider_installation {\n filesystem_mirror {\n path = %q\n }\n}\n", mirror)), 0600); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"version": 4, "terraform_version": "1.11.0", "serial": 1, "lineage": "33333333-2222-4333-8444-555555555555", "outputs": map[string]any{}, "resources": []any{
		map[string]any{"mode": "managed", "type": "ory_platform_identity", "name": "existing", "provider": `provider["registry.opentofu.org/nitra/ory"]`, "instances": []any{
			map[string]any{"schema_version": 0, "attributes": map[string]any{"id": platformID, "document": string(initial.Document), "observed_document": string(initial.Document), "revision": initial.Revision}, "sensitive_attributes": []any{
				[]any{map[string]any{"type": "get_attr", "value": "document"}}, []any{map[string]any{"type": "get_attr", "value": "observed_document"}},
			}},
		}},
	}}
	stateBytes, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "terraform.tfstate")
	if err := os.WriteFile(statePath, stateBytes, 0600); err != nil {
		t.Fatal(err)
	}
	writeConfig := func(allow string, updated bool) {
		t.Helper()
		desired := map[string]any{}
		for key, value := range doc {
			desired[key] = value
		}
		if updated {
			desired["state"] = "inactive"
		}
		encoded, err := json.Marshal(desired)
		if err != nil {
			t.Fatal(err)
		}
		config := fmt.Sprintf(`terraform {
 required_providers { hydra = { source = "nitra/ory", version = "99.0.0" } }
}
provider "hydra" {
 kratos_admin = %q
 %s
}
resource "ory_platform_identity" "existing" {
 provider = hydra
 document = %q
 lifecycle { prevent_destroy = true }
}
`, remote, allow, string(encoded))
		if err := os.WriteFile(filepath.Join(root, "main.tf"), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(expected int, args ...string) []byte {
		t.Helper()
		if len(args) == 0 || args[0] != "init" && args[0] != "plan" && args[0] != "show" {
			t.Fatal("only init/plan/show authorized")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, tofu, args...)
		command.Dir = root
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "TF_CLI_CONFIG_FILE=" + cli, "TF_INPUT=0", "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1", "KRATOS_ADMIN_ALLOWED_REMOTE_ENDPOINTS=" + remote}
		output, err := command.CombinedOutput()
		status := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				status = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if status != expected {
			t.Fatalf("tofu %s exit %d, expected %d; output omitted", args[0], status, expected)
		}
		after, err := os.ReadFile(statePath)
		if err != nil || !bytes.Equal(after, stateBytes) {
			t.Fatal("local state bytes changed")
		}
		if writes.Load() != 0 || redirected.Load() != 0 {
			t.Fatal("mutation or redirect target reached")
		}
		return output
	}
	writeConfig("", false)
	run(0, "init", "-input=false", "-plugin-dir="+mirror)
	before := reads.Load()
	denied := run(1, "plan", "-input=false", "-detailed-exitcode")
	if !bytes.Contains(denied, []byte("kratos_admin_allowed_remote_endpoints")) || reads.Load() != before {
		t.Fatal("default remote policy not enforced before IO")
	}
	writeConfig(`kratos_admin_allowed_remote_endpoints = ["http://other.invalid:8080"]`, false)
	run(1, "plan", "-input=false", "-detailed-exitcode")
	if reads.Load() != before {
		t.Fatal("non-matching allowlist reached API")
	}
	allow := fmt.Sprintf("kratos_admin_allowed_remote_endpoints = [%q]", remote)
	for _, updated := range []bool{false, true} {
		writeConfig(allow, updated)
		expected := 0
		if updated {
			expected = 2
		}
		plan := filepath.Join(root, "reviewed.tfplan")
		run(expected, "plan", "-input=false", "-detailed-exitcode", "-out="+plan)
		shown := run(0, "show", "-json", plan)
		var parsed struct {
			ResourceChanges []struct {
				Change struct {
					Actions []string `json:"actions"`
				} `json:"change"`
			} `json:"resource_changes"`
		}
		if err := json.Unmarshal(shown, &parsed); err != nil {
			t.Fatal(err)
		}
		action := "no-op"
		if updated {
			action = "update"
		}
		if len(parsed.ResourceChanges) != 1 || len(parsed.ResourceChanges[0].Change.Actions) != 1 || parsed.ResourceChanges[0].Change.Actions[0] != action || bytes.Contains(shown, []byte("sample-secret")) {
			t.Fatal("unexpected plan action or credential projection")
		}
	}
	if reads.Load() <= before {
		t.Fatal("allowlisted plans did not read fixture")
	}
	redirect.Store(true)
	run(1, "plan", "-input=false", "-detailed-exitcode")
}
