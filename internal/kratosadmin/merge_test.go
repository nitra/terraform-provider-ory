package kratosadmin

import (
	"encoding/json"
	"strings"
	"testing"
)

func doc(traits map[string]any) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"schema_id": "default", "state": "active", "traits": traits,
		"metadata_public": nil, "metadata_admin": nil,
		"verifiable_addresses": []any{}, "recovery_addresses": []any{},
		"external_id": nil, "organization_id": nil,
	})
	return data
}

func TestMergeIndependentPathsAndConflict(t *testing.T) {
	previous := doc(map[string]any{"email": "old@example.com", "name": "Old"})
	desired := doc(map[string]any{"email": "new@example.com", "name": "Old"})
	live := doc(map[string]any{"email": "old@example.com", "name": "Edited outside"})
	changes, err := Merge(previous, desired, live)
	if err != nil || len(changes) != 1 || changes[0].Path != "/traits/email" || changes[0].Value != "new@example.com" {
		t.Fatalf("independent merge: %#v, %v", changes, err)
	}
	conflicting := doc(map[string]any{"email": "outside@example.com", "name": "Old"})
	if _, err := Merge(previous, desired, conflicting); err == nil || !strings.Contains(err.Error(), "/traits/email") {
		t.Fatalf("same-path edit must conflict: %v", err)
	}
}

func TestMergeArraysAreAtomicAndEscapesPaths(t *testing.T) {
	previous := doc(map[string]any{"email": "a@example.com", "labels": []any{"a"}, "a/b~c": "before"})
	desired := doc(map[string]any{"email": "a@example.com", "labels": []any{"b"}, "a/b~c": "after"})
	live := doc(map[string]any{"email": "a@example.com", "labels": []any{"outside"}, "a/b~c": "before"})
	if _, err := Merge(previous, desired, live); err == nil || !strings.Contains(err.Error(), "/traits/labels") {
		t.Fatalf("array conflict missing: %v", err)
	}
	changes, err := Merge(previous, desired, previous)
	if err != nil || len(changes) != 2 || changes[0].Path != "/traits/a~1b~0c" {
		t.Fatalf("escaped paths: %#v, %v", changes, err)
	}
}

func TestMergeIndependentMetadataFromNull(t *testing.T) {
	previous := doc(map[string]any{"email": "a@example.com"})
	var desired, live map[string]any
	_ = json.Unmarshal(previous, &desired)
	_ = json.Unmarshal(previous, &live)
	desired["metadata_admin"] = map[string]any{"approval": "operator"}
	live["metadata_admin"] = map[string]any{"external": "service"}
	wanted, _ := json.Marshal(desired)
	actual, _ := json.Marshal(live)
	changes, err := Merge(previous, wanted, actual)
	if err != nil || len(changes) != 1 || changes[0].Path != "/metadata_admin/approval" {
		t.Fatalf("null metadata merge: %#v, %v", changes, err)
	}
}
