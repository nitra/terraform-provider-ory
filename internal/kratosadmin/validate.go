package kratosadmin

import (
	"encoding/json"
	"errors"
)

// ValidateDocument excludes credentials and server-owned identity fields.
func ValidateDocument(data []byte) error {
	var v map[string]json.RawMessage
	if json.Unmarshal(data, &v) != nil {
		return errors.New("document має бути JSON object")
	}
	fields := []string{"schema_id", "state", "traits", "metadata_public", "metadata_admin", "verifiable_addresses", "recovery_addresses", "external_id", "organization_id"}
	if len(v) != len(fields) {
		return errors.New("document має містити лише дев’ять керованих полів; credentials і read-only поля заборонені")
	}
	for _, key := range fields {
		if _, ok := v[key]; !ok {
			return errors.New("document не містить усіх керованих полів")
		}
	}
	var schema, state string
	if json.Unmarshal(v["schema_id"], &schema) != nil || schema == "" || json.Unmarshal(v["state"], &state) != nil || (state != "active" && state != "inactive") {
		return errors.New("некоректні schema_id/state")
	}
	var traits map[string]json.RawMessage
	if json.Unmarshal(v["traits"], &traits) != nil || traits == nil {
		return errors.New("traits має бути object")
	}
	for _, key := range []string{"metadata_admin", "metadata_public"} {
		if string(v[key]) == "null" {
			continue
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(v[key], &object) != nil || object == nil {
			return errors.New("metadata має бути object або null")
		}
	}
	for _, key := range []string{"verifiable_addresses", "recovery_addresses"} {
		var array []json.RawMessage
		if json.Unmarshal(v[key], &array) != nil || array == nil {
			return errors.New("addresses мають бути arrays")
		}
		for _, address := range array {
			var object map[string]json.RawMessage
			if json.Unmarshal(address, &object) != nil || object == nil {
				return errors.New("address має бути object")
			}
			for _, field := range []string{"id", "created_at", "updated_at"} {
				if _, ok := object[field]; ok {
					return errors.New("server-owned address fields заборонені")
				}
			}
		}
	}
	for _, key := range []string{"external_id", "organization_id"} {
		if string(v[key]) == "null" {
			continue
		}
		var value string
		if json.Unmarshal(v[key], &value) != nil {
			return errors.New("external_id/organization_id мають бути string або null")
		}
	}
	return nil
}
