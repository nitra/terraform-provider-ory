package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nitra/terraform-provider-ory/internal/adminapi"
)

const externalID = "00000000-0000-4000-8000-000000000003"

// TestAccExternalUserLifecycle використовує справжній OpenTofu і mock execution API.
// Перевірка backend authorization лишається окремим інтеграційним етапом.
func TestAccExternalUserLifecycle(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("потрібен TF_ACC=1 і OpenTofu")
	}
	var mu sync.Mutex
	var current *adminapi.User
	creates, deletes, denyReads := 0, 0, false
	var keys []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer sample-secret" {
			t.Error("неправильна авторизація")
			w.WriteHeader(401)
			return
		}
		want := "/v1/organizations/abie-ua/users"
		if r.Method != "POST" {
			want += "/" + externalID
		}
		if r.URL.Path != want {
			t.Errorf("неочікуваний path: %s", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		if r.Method == "GET" && denyReads {
			w.WriteHeader(403)
			return
		}
		if r.Method != "POST" && current == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"identity_not_found"}}`))
			return
		}
		switch r.Method {
		case "POST":
			if current != nil {
				t.Error("повторне створення наявної identity")
				w.WriteHeader(409)
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if _, err := uuid.Parse(key); err != nil {
				t.Error("відсутній UUID idempotency key")
			}
			keys = append(keys, key)
			var body struct {
				Email string  `json:"email"`
				Name  *string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			current = &adminapi.User{ID: externalID, Email: body.Email, Name: body.Name, OrganizationID: "abie-ua", State: "active"}
			creates++
			w.WriteHeader(201)
		case "DELETE":
			var body struct {
				Email  string `json:"confirm_email"`
				Reason string `json:"reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Email != current.Email || body.Reason != "Погоджене видалення" {
				t.Error("неправильне підтвердження DELETE")
			}
			current = nil
			deletes++
			w.WriteHeader(204)
			return
		case "GET":
		default:
			t.Errorf("заборонений method %s", r.Method)
			w.WriteHeader(405)
			return
		}
		_ = json.NewEncoder(w).Encode(current)
	}))
	defer s.Close()
	file := filepath.Join(t.TempDir(), "access.token")
	if err := os.WriteFile(file, []byte("sample-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	config := func(org, email, name, reason string) string {
		return fmt.Sprintf(`
provider "hydra" {
  user_api {
    endpoint = %q
    token_file = %q
  }
}
resource "ory_external_user" "test" {
  provider = hydra
  organization_id = %q
  create_request_id = "00000000-0000-4000-8000-000000000004"
  email = %q
  name = %q
  deletion_reason = %q
}`, s.URL, file, org, email, name, reason)
	}
	initial := config("abie-ua", "example@example.com", "Приклад", defaultDeletionReason)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{Config: initial, Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("ory_external_user.test", "id", externalID),
				resource.TestCheckResourceAttr("ory_external_user.test", "state", "active"),
				resource.TestCheckResourceAttrSet("ory_external_user.test", "create_request_id"),
			)},
			{Config: initial, PlanOnly: true},
			{ResourceName: "ory_external_user.test", ImportState: true, ImportStateId: "abie-ua/" + externalID + "/00000000-0000-4000-8000-000000000004", ImportStateVerify: true},
			{Config: config("abie-ua", "other@example.com", "Приклад", defaultDeletionReason), PlanOnly: true, ExpectError: regexp.MustCompile("Зміна identity заборонена")},
			{Config: config("other-org", "example@example.com", "Приклад", defaultDeletionReason), PlanOnly: true, ExpectError: regexp.MustCompile("Зміна identity заборонена")},
			{Config: config("abie-ua", "example@example.com", "Інше ім’я", defaultDeletionReason), PlanOnly: true, ExpectError: regexp.MustCompile("Зміна identity заборонена")},
			{PreConfig: func() { mu.Lock(); denyReads = true; mu.Unlock() }, Config: initial, PlanOnly: true, ExpectError: regexp.MustCompile("HTTP 403")},
			{PreConfig: func() { mu.Lock(); denyReads = false; mu.Unlock() }, Config: config("abie-ua", "example@example.com", "Приклад", "Погоджене видалення")},
		},
	})
	mu.Lock()
	defer mu.Unlock()
	if creates != 1 || deletes != 1 || len(keys) != 1 {
		t.Fatalf("неочікувані mutations: create=%d delete=%d keys=%d", creates, deletes, len(keys))
	}
}
