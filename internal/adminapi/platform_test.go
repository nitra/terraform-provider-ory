package adminapi

import (
	"encoding/json"
	"testing"
)

func TestPlatformDocumentRejectsCredentialsAndServerOwnedFields(t *testing.T) {
	valid := map[string]any{"schema_id": "default", "state": "active", "traits": map[string]any{"email": "test@example.com"}, "metadata_admin": nil, "metadata_public": nil, "verifiable_addresses": []any{}, "recovery_addresses": []any{}, "external_id": nil, "organization_id": nil}
	data, _ := json.Marshal(valid)
	if err := ValidatePlatformDocument(data); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"credentials", "id", "updated_at", "schema_url"} {
		valid[key] = "sample-secret"
		data, _ = json.Marshal(valid)
		if ValidatePlatformDocument(data) == nil {
			t.Fatalf("accepted forbidden field %s", key)
		}
		delete(valid, key)
	}
	valid["state"] = "invalid"
	data, _ = json.Marshal(valid)
	if ValidatePlatformDocument(data) == nil {
		t.Fatal("accepted invalid state")
	}
}
