package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const platformID = "00000000-0000-4000-8000-000000000001"

// Runs actual OpenTofu import, update and zero-change plans against an isolated API.
func TestAccPlatformIdentityImportUpdate(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC=1 requires OpenTofu")
	}
	tofu, err := exec.LookPath("tofu")
	if err != nil {
		t.Skip("OpenTofu unavailable")
	}
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("sample-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"schema_id": "default", "state": "active", "traits": map[string]any{"email": "admin@example.com", "user_type": "internal"}, "metadata_admin": map[string]any{"memberships": map[string]any{"org": map[string]any{"role": "admin"}}}, "metadata_public": map[string]any{"roles": []string{"admin"}}, "verifiable_addresses": []any{}, "recovery_addresses": []any{}, "external_id": nil, "organization_id": nil}
	var mu sync.Mutex
	writes := 0
	revision := strings.Repeat("a", 64)
	deny := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer sample-secret" || r.URL.Path != "/v1/platform/identities/"+platformID {
			w.WriteHeader(403)
			return
		}
		if deny {
			w.WriteHeader(403)
			return
		}
		if r.Method == "PUT" {
			var body struct {
				Document map[string]any `json:"document"`
				Revision string         `json:"expected_revision"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Revision != revision {
				w.WriteHeader(409)
				return
			}
			if _, ok := body.Document["credentials"]; ok {
				t.Error("credential injection")
				w.WriteHeader(422)
				return
			}
			doc = body.Document
			writes++
			revision = strings.Repeat("b", 64)
		} else if r.Method != "GET" {
			t.Errorf("unexpected mutation %s", r.Method)
			w.WriteHeader(405)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": platformID, "document": doc, "revision": revision})
	}))
	defer s.Close()
	mirror := filepath.Join(dir, "mirror")
	plugin := filepath.Join(mirror, "registry.opentofu.org", "nitra", "ory", "99.0.0", runtime.GOOS+"_"+runtime.GOARCH, "terraform-provider-ory_v99.0.0")
	if err := os.MkdirAll(filepath.Dir(plugin), 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", plugin, "../..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cli := filepath.Join(dir, "tofu.rc")
	if err := os.WriteFile(cli, []byte(fmt.Sprintf("provider_installation {\n filesystem_mirror {\n path = %q\n }\n }", mirror)), 0600); err != nil {
		t.Fatal(err)
	}
	writeConfig := func(document any) {
		t.Helper()
		encoded, _ := json.Marshal(document)
		config := fmt.Sprintf(`terraform {
 required_providers {
 hydra = { source = "nitra/ory", version = "99.0.0" }
 }
}
provider "hydra" {
 platform_api {
 endpoint = %q
 token_file = %q
 }
}
resource "ory_platform_identity" "user" {
 provider = hydra
 document = %q
 lifecycle { prevent_destroy = true }
}
`, s.URL, token, string(encoded))
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(expected int, args ...string) string {
		t.Helper()
		cmd := exec.Command(tofu, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+cli, "TF_IN_AUTOMATION=1")
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != expected {
			t.Fatalf("tofu %v: exit %d, want %d\n%s", args, code, expected, out)
		}
		return string(out)
	}
	writeConfig(doc)
	run(0, "init", "-input=false")
	run(0, "import", "-input=false", "ory_platform_identity.user", platformID)
	run(0, "plan", "-input=false", "-detailed-exitcode")
	if writes != 0 {
		t.Fatal("import mutated identity")
	}
	desired := map[string]any{}
	for k, v := range doc {
		desired[k] = v
	}
	desired["state"] = "inactive"
	desired["traits"] = map[string]any{"email": "changed@example.com", "name": "Changed", "user_type": "internal", "picture": "https://example.com/avatar.png"}
	desired["metadata_admin"] = map[string]any{"memberships": map[string]any{"org": map[string]any{"role": "member"}}}
	desired["metadata_public"] = map[string]any{"roles": []string{"abie:dagster"}}
	writeConfig(desired)
	run(2, "plan", "-input=false", "-detailed-exitcode", "-out=update.plan")
	run(0, "apply", "-input=false", "update.plan")
	run(0, "plan", "-input=false", "-detailed-exitcode")
	if writes != 1 {
		t.Fatalf("writes = %d", writes)
	}
	desired["state"] = "active"
	writeConfig(desired)
	run(2, "plan", "-input=false", "-detailed-exitcode", "-out=race.plan")
	mu.Lock()
	revision = strings.Repeat("c", 64)
	mu.Unlock()
	run(1, "apply", "-input=false", "race.plan")
	if writes != 1 {
		t.Fatal("stale saved plan performed a write")
	}
	mu.Lock()
	deny = true
	mu.Unlock()
	run(1, "plan", "-input=false", "-detailed-exitcode")
	state, err := os.ReadFile(filepath.Join(dir, "terraform.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), platformID) || strings.Contains(string(state), "sample-secret") {
		t.Fatal("UUID lost or credential leaked to state")
	}
}
