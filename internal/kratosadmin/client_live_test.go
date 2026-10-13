package kratosadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This test creates and removes only its fixture in an isolated loopback Kratos.
func TestIsolatedKratosThreeWayMerge(t *testing.T) {
	endpoint := os.Getenv("ORY_KRATOS_TEST_URL")
	if endpoint == "" {
		t.Skip("ORY_KRATOS_TEST_URL is required")
	}
	client, err := New(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	email := "fixture-" + uuid.NewString() + "@example.com"
	created := map[string]any{
		"schema_id": "default", "state": "active",
		"traits":          map[string]any{"email": email, "name": "Before", "user_type": "internal"},
		"metadata_public": map[string]any{"roles": []string{"admin"}},
		"credentials":     map[string]any{"password": map[string]any{"config": map[string]any{"password": "sample-secret-password-123456789"}}},
	}
	encoded, _ := json.Marshal(created)
	response, err := httpClient.Post(endpoint+"/admin/identities", "application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("fixture create HTTP %d: %s", response.StatusCode, data)
	}
	var raw map[string]any
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	id := raw["id"].(string)
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, endpoint+"/admin/identities/"+id, nil)
		res, err := httpClient.Do(req)
		if err != nil || res.StatusCode != http.StatusNoContent {
			t.Errorf("fixture cleanup failed: %v", err)
		}
		if res != nil {
			res.Body.Close()
		}
	})
	ctx := context.Background()
	before, err := client.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var desired map[string]any
	if err := json.Unmarshal(before.Document, &desired); err != nil {
		t.Fatal(err)
	}
	desired["state"] = "inactive"
	desired["traits"].(map[string]any)["email"] = "changed-" + email
	desired["metadata_public"].(map[string]any)["roles"] = []string{"member"}
	newDocument, _ := json.Marshal(desired)
	external := []Operation{{Op: "add", Path: "/traits/name", Value: "Outside"}}
	if _, err := client.Patch(ctx, id, external); err != nil {
		t.Fatal(err)
	}
	live, err := client.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := Merge(before.Document, newDocument, live.Document)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("expected 3 independent changes, got %d", len(changes))
	}
	after, err := client.Patch(ctx, id, changes)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	_ = json.Unmarshal(after.Document, &actual)
	if actual["state"] != "inactive" || actual["traits"].(map[string]any)["name"] != "Outside" || actual["traits"].(map[string]any)["email"] != desired["traits"].(map[string]any)["email"] {
		t.Fatalf("independent changes were not preserved: %s", after.Document)
	}
	conflict, err := client.Patch(ctx, id, []Operation{{Op: "add", Path: "/traits/email", Value: "outside-" + email}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(before.Document, newDocument, conflict.Document); err == nil {
		t.Fatal("stale same-path change did not conflict")
	}
	var withoutName map[string]any
	_ = json.Unmarshal(conflict.Document, &withoutName)
	delete(withoutName["traits"].(map[string]any), "name")
	withoutNameJSON, _ := json.Marshal(withoutName)
	removal, err := Merge(conflict.Document, withoutNameJSON, conflict.Document)
	if err != nil || len(removal) != 1 || removal[0].Path != "/traits/name" || removal[0].Op != "remove" {
		t.Fatalf("nested remove plan: %#v, %v", removal, err)
	}
	removed, err := client.Patch(ctx, id, removal)
	if err != nil {
		t.Fatal(err)
	}
	var removedDoc map[string]any
	_ = json.Unmarshal(removed.Document, &removedDoc)
	if _, exists := removedDoc["traits"].(map[string]any)["name"]; exists {
		t.Fatal("nested trait remove was ignored")
	}
	res, err := httpClient.Get(fmt.Sprintf("%s/admin/identities/%s?include_credential=password", endpoint, id))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var withCredentials map[string]any
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&withCredentials) != nil || withCredentials["credentials"] == nil {
		t.Fatal("password credential was lost")
	}
}
