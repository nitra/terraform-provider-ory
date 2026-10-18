package kratosadmin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRemoteEndpointRequiresExactAllowlist(t *testing.T) {
	const endpoint = "http://platform-identities-read:8080"
	for _, allowed := range [][]string{nil, {}, {"https://platform-identities-read:8080"}, {"http://platform-identities-read:8081"}, {"http://other:8080"}, {"http://platform-identities-read:8080.evil"}} {
		if _, err := NewWithAllowedRemoteEndpoints(endpoint, allowed); err == nil {
			t.Fatalf("accepted non-matching remote allowlist")
		}
	}
	for _, endpoint := range []string{"http://platform-identities-read:8080", "https://kratos.example.invalid:443", "http://[2001:db8::1]:8080"} {
		client, err := NewWithAllowedRemoteEndpoints(endpoint, []string{endpoint})
		if err != nil || client.base != endpoint {
			t.Fatalf("exact opt-in failed: %v", err)
		}
		transport, ok := client.http.Transport.(*http.Transport)
		if !ok || transport.Proxy != nil || transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
			t.Fatal("unsafe HTTP transport")
		}
	}
}

func TestInvalidRemoteAllowlistIsRejectedEvenForLoopback(t *testing.T) {
	for _, endpoint := range []string{"", "kratos:8080", "ftp://kratos", "http://*.svc:8080", "http://kratos/path", "http://kratos/", "http://user:sample-secret@kratos", "http://kratos?", "http://kratos?x=1", "http://kratos#", "http://kratos#fragment", "http://:8080"} {
		if _, err := NewWithAllowedRemoteEndpoints("http://127.0.0.1:8080", []string{endpoint}); err == nil {
			t.Errorf("accepted malformed allowlist entry %q", endpoint)
		}
		if _, err := NewWithAllowedRemoteEndpoints(endpoint, []string{endpoint}); err == nil {
			t.Errorf("accepted malformed target %q", endpoint)
		}
	}
}

func TestDefaultLoopbackDoesNotRequireRemoteOptIn(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:8080", "https://localhost:8443", "http://[::1]:8080"} {
		if _, err := New(endpoint); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := New("https://kratos.example.invalid"); err == nil {
		t.Fatal("default accepted remote HTTPS")
	}
}

func TestRemoteClientReadAndRedirectStayScoped(t *testing.T) {
	var reads, mutations, redirected atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	var redirect atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
			w.WriteHeader(405)
			return
		}
		reads.Add(1)
		if r.URL.Path != "/admin/identities/"+fixtureID || r.URL.RawQuery != "" {
			t.Error("unexpected request path")
			w.WriteHeader(404)
			return
		}
		if redirect.Load() {
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fixtureID, "schema_id": "default", "state": "active", "traits": map[string]any{"name": "Synthetic"}, "verifiable_addresses": []any{}, "recovery_addresses": []any{}})
	}))
	defer server.Close()
	const endpoint = "http://kratos.fixture.invalid:8080"
	client, err := NewWithAllowedRemoteEndpoints(endpoint, []string{endpoint})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.http.Transport.(*http.Transport)
	// Route only this synthetic hostname to the local test listener; no DNS or remote IO.
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "kratos.fixture.invalid:8080" {
			t.Errorf("unexpected destination %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
	}
	if _, err := client.Read(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	redirect.Store(true)
	if _, err := client.Read(context.Background(), fixtureID); err == nil {
		t.Fatal("followed or accepted redirect")
	}
	if reads.Load() != 2 || mutations.Load() != 0 || redirected.Load() != 0 {
		t.Fatalf("unexpected request counts: reads=%d mutations=%d redirected=%d", reads.Load(), mutations.Load(), redirected.Load())
	}
}
