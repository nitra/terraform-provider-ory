// Package kratosadmin implements read and narrow JSON Patch access to Kratos Admin.
package kratosadmin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("identity не існує")

type Identity struct {
	ID        string
	Document  json.RawMessage
	Revision  string
	addresses map[string][]map[string]json.RawMessage
}

type Operation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

type Client struct {
	base string
	http *http.Client
}

// New preserves the loopback-only default for local kubectl port-forward.
func New(endpoint string) (*Client, error) {
	return NewWithAllowedRemoteEndpoints(endpoint, nil)
}

// endpointURL accepts only bare HTTP(S) origins, never paths or URL credentials.
func endpointURL(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Opaque != "" || strings.ContainsAny(endpoint, "*?#") || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("Kratos Admin endpoint має бути абсолютним URL без path, credentials, query і fragment")
	}
	return u, nil
}

// NewWithAllowedRemoteEndpoints opts in only exact trusted remote base URLs.
// This is not authentication, DNS pinning, a network sandbox or apply authorization.
func NewWithAllowedRemoteEndpoints(endpoint string, allowed []string) (*Client, error) {
	for _, candidate := range allowed {
		if _, err := endpointURL(candidate); err != nil {
			return nil, errors.New("Некоректний kratos_admin_allowed_remote_endpoints; потрібні exact HTTP(S) base URLs без wildcard, path, credentials, query і fragment")
		}
	}
	u, err := endpointURL(endpoint)
	if err != nil {
		return nil, err
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	permitted := loopback
	for _, candidate := range allowed {
		permitted = permitted || candidate == endpoint
	}
	if !permitted {
		return nil, errors.New("Kratos Admin remote endpoint потребує exact kratos_admin_allowed_remote_endpoints; default дозволяє лише loopback port-forward")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Trusted executor URLs must not be rerouted by inherited HTTP(S)_PROXY.
	transport.Proxy = nil
	return &Client{base: strings.TrimRight(endpoint, "/"), http: &http.Client{
		Transport:     transport,
		Timeout:       15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) request(ctx context.Context, method, id string, body any) (Identity, error) {
	var result Identity
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return result, errors.New("identity ID має бути canonical UUID")
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return result, errors.New("не вдалося сформувати Kratos PATCH")
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/admin/identities/"+id, payload)
	if err != nil {
		return result, errors.New("не вдалося створити Kratos запит")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return result, errors.New("Kratos Admin недоступний; результат PATCH може бути невідомим, перечитайте identity")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return result, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("Kratos Admin HTTP %d; response body прихований", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return result, errors.New("Kratos повернув некоректну або завелику identity")
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || json.Unmarshal(raw["id"], &result.ID) != nil || result.ID != id {
		return result, errors.New("Kratos повернув інший UUID або некоректну identity")
	}
	document := make(map[string]json.RawMessage, 9)
	result.addresses = make(map[string][]map[string]json.RawMessage, 2)
	for _, field := range []string{"schema_id", "state", "traits", "metadata_public", "metadata_admin", "verifiable_addresses", "recovery_addresses", "external_id", "organization_id"} {
		value, ok := raw[field]
		if !ok {
			value = json.RawMessage("null")
			if strings.HasSuffix(field, "_addresses") {
				value = json.RawMessage("[]")
			}
		}
		if strings.HasSuffix(field, "_addresses") {
			var addresses []map[string]json.RawMessage
			if json.Unmarshal(value, &addresses) != nil || addresses == nil {
				return result, errors.New("Kratos повернув некоректні addresses")
			}
			for _, address := range addresses {
				result.addresses[field] = append(result.addresses[field], maps.Clone(address))
			}
			for _, address := range addresses {
				delete(address, "id")
				delete(address, "created_at")
				delete(address, "updated_at")
			}
			value, err = json.Marshal(addresses)
			if err != nil {
				return result, errors.New("не вдалося прочитати addresses")
			}
		}
		document[field] = value
	}
	result.Document, err = json.Marshal(document)
	if err != nil || ValidateDocument(result.Document) != nil {
		return Identity{}, errors.New("Kratos повернув некоректні керовані поля")
	}
	// Terraform compares configured strings during import. Sort nested object
	// keys too, so jsonencode(HCL) matches the imported JSON document.
	decoder := json.NewDecoder(bytes.NewReader(result.Document))
	decoder.UseNumber()
	var canonical any
	if decoder.Decode(&canonical) != nil {
		return Identity{}, errors.New("не вдалося нормалізувати identity document")
	}
	result.Document, err = json.Marshal(canonical)
	if err != nil {
		return Identity{}, errors.New("не вдалося нормалізувати identity document")
	}
	fingerprint, err := json.Marshal(struct {
		Document json.RawMessage `json:"document"`
		Updated  json.RawMessage `json:"updated_at"`
	}{result.Document, raw["updated_at"]})
	if err != nil {
		return Identity{}, errors.New("не вдалося обчислити revision")
	}
	result.Revision = fmt.Sprintf("%x", sha256.Sum256(fingerprint))
	return result, nil
}

func (c *Client) Read(ctx context.Context, id string) (Identity, error) {
	return c.request(ctx, http.MethodGet, id, nil)
}

// Patch performs a single mutation without automatic retries.
func (c *Client) Patch(ctx context.Context, id string, operations []Operation) (Identity, error) {
	return c.PatchWithCurrent(ctx, id, operations, Identity{})
}

// PatchWithCurrent keeps server-owned fields on unchanged address entries.
func (c *Client) PatchWithCurrent(ctx context.Context, id string, operations []Operation, current Identity) (Identity, error) {
	if len(operations) == 0 {
		return c.Read(ctx, id)
	}
	prepared := make([]Operation, len(operations))
	copy(prepared, operations)
	for index := range prepared {
		operation := &prepared[index]
		if operation.Path != "/verifiable_addresses" && operation.Path != "/recovery_addresses" || operation.Op == "remove" {
			continue
		}
		if current.ID != id {
			return Identity{}, errors.New("потрібен поточний Kratos документ для зміни addresses")
		}
		encoded, err := json.Marshal(operation.Value)
		if err != nil {
			return Identity{}, errors.New("некоректні addresses")
		}
		var desired []map[string]json.RawMessage
		if json.Unmarshal(encoded, &desired) != nil || desired == nil {
			return Identity{}, errors.New("некоректні addresses")
		}
		for _, address := range desired {
			for _, prior := range current.addresses[strings.TrimPrefix(operation.Path, "/")] {
				if bytes.Equal(address["value"], prior["value"]) && bytes.Equal(address["via"], prior["via"]) {
					for _, field := range []string{"id", "created_at", "updated_at"} {
						if value, ok := prior[field]; ok {
							address[field] = value
						}
					}
					break
				}
			}
		}
		operation.Value = desired
	}
	return c.request(ctx, http.MethodPatch, id, prepared)
}
