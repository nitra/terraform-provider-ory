package kratosadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const fixtureID = "00000000-0000-4000-8000-000000000001"

func TestPatchPreservesUnchangedAddressServerFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/identities/"+fixtureID {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method == http.MethodPatch {
			var operations []Operation
			if err := json.NewDecoder(r.Body).Decode(&operations); err != nil {
				t.Error(err)
			}
			var value []map[string]any
			encoded, _ := json.Marshal(operations[0].Value)
			_ = json.Unmarshal(encoded, &value)
			if value[0]["id"] != "server-id" || value[0]["created_at"] != "server-created" || value[1]["id"] != nil {
				t.Errorf("server fields were not preserved selectively: %#v", value)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": fixtureID, "schema_id": "default", "state": "active",
			"traits":          map[string]any{"email": "a@example.com"},
			"metadata_public": nil, "metadata_admin": nil,
			"verifiable_addresses": []any{map[string]any{"id": "server-id", "created_at": "server-created", "value": "a@example.com", "via": "email"}},
			"recovery_addresses":   []any{}, "external_id": nil, "organization_id": nil,
		})
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	current, err := client.Read(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PatchWithCurrent(context.Background(), fixtureID, []Operation{{Op: "add", Path: "/verifiable_addresses", Value: []any{
		map[string]any{"value": "a@example.com", "via": "email"},
		map[string]any{"value": "b@example.com", "via": "email"},
	}}}, current); err != nil {
		t.Fatal(err)
	}
}

func TestKratosAdminEndpointRequiresLoopback(t *testing.T) {
	for _, endpoint := range []string{"http://kratos-admin:80", "https://example.com", "http://127.0.0.1:1234/admin", "http://user:pass@127.0.0.1:1234"} {
		if _, err := New(endpoint); err == nil {
			t.Errorf("accepted unsafe endpoint %s", endpoint)
		}
	}
}

func TestReadCanonicalizesNestedJSONForImportPlan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"00000000-0000-4000-8000-000000000001","schema_id":"default","state":"active","traits":{"z":"last","a":"first"},"metadata_public":null,"metadata_admin":null,"verifiable_addresses":[],"recovery_addresses":[],"external_id":null,"organization_id":null}`))
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := client.Read(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if string(identity.Document) != `{"external_id":null,"metadata_admin":null,"metadata_public":null,"organization_id":null,"recovery_addresses":[],"schema_id":"default","state":"active","traits":{"a":"first","z":"last"},"verifiable_addresses":[]}` {
		t.Fatalf("document is not canonical: %s", identity.Document)
	}
}
