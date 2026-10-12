package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// PlatformIdentity carries only the configurable document, never credential material.
type PlatformIdentity struct {
	ID       string          `json:"id"`
	Document json.RawMessage `json:"document"`
	Revision string          `json:"revision"`
}

// ValidatePlatformDocument prevents secrets or read-only fields from entering state.
func ValidatePlatformDocument(data []byte) error {
	var v map[string]json.RawMessage
	if json.Unmarshal(data, &v) != nil {
		return errors.New("document має бути JSON object")
	}
	fields := []string{"schema_id", "state", "traits", "metadata_public", "metadata_admin", "verifiable_addresses", "recovery_addresses", "external_id", "organization_id"}
	if len(v) != len(fields) {
		return errors.New("document має містити лише дев’ять керованих полів; credentials і read-only поля заборонені")
	}
	for _, k := range fields {
		if _, ok := v[k]; !ok {
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
	for _, k := range []string{"metadata_admin", "metadata_public"} {
		if string(v[k]) == "null" {
			continue
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(v[k], &object) != nil || object == nil {
			return errors.New("metadata має бути object або null")
		}
	}
	for _, k := range []string{"verifiable_addresses", "recovery_addresses"} {
		var array []json.RawMessage
		if json.Unmarshal(v[k], &array) != nil || array == nil {
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
	for _, k := range []string{"external_id", "organization_id"} {
		if string(v[k]) == "null" {
			continue
		}
		var s string
		if json.Unmarshal(v[k], &s) != nil {
			return errors.New("external_id/organization_id мають бути string або null")
		}
	}
	return nil
}

func platformPath(id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", errors.New("identity ID має бути UUID")
	}
	return "/v1/platform/identities/" + parsed.String(), nil
}

func validatePlatform(identity PlatformIdentity, id string) error {
	if identity.ID != id || len(identity.Revision) != 64 {
		return errors.New("platform API повернув інший UUID або некоректну revision")
	}
	return ValidatePlatformDocument(identity.Document)
}

// ReadPlatform uses authoritative point reads; permission errors preserve state.
func (c *Client) ReadPlatform(ctx context.Context, id string) (PlatformIdentity, error) {
	var result PlatformIdentity
	p, err := platformPath(id)
	if err != nil {
		return result, err
	}
	err = c.request(ctx, http.MethodGet, p, nil, "", http.StatusOK, &result)
	if err == nil {
		err = validatePlatform(result, id)
	}
	return result, err
}

// UpdatePlatform sends the expected revision and performs no implicit retry.
func (c *Client) UpdatePlatform(ctx context.Context, id string, document json.RawMessage, revision string) (PlatformIdentity, error) {
	var result PlatformIdentity
	if err := ValidatePlatformDocument(document); err != nil {
		return result, err
	}
	p, err := platformPath(id)
	if err != nil {
		return result, err
	}
	err = c.request(ctx, http.MethodPut, p, struct {
		Document json.RawMessage `json:"document"`
		Revision string          `json:"expected_revision"`
	}{document, revision}, "", http.StatusOK, &result)
	if err == nil {
		err = validatePlatform(result, id)
	}
	return result, err
}
