package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testID = "00000000-0000-4000-8000-000000000001"

// tokenFile створює лише тимчасовий fixture token без реальних credentials.
func tokenFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "access.token")
	if err := os.WriteFile(p, []byte("sample-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestClientLifecycle перевіряє methods, paths, підтвердження email й ключ запиту.
func TestClientLifecycle(t *testing.T) {
	var methods []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Header.Get("Authorization") != "Bearer sample-secret" {
			t.Error("token відсутній")
		}
		want := "/v1/organizations/abie-ua/users"
		if r.Method != http.MethodPost {
			want += "/" + testID
		}
		if r.URL.Path != want {
			t.Errorf("path: %q", r.URL.Path)
		}
		switch r.Method {
		case http.MethodPost:
			if r.Header.Get("Idempotency-Key") != testID {
				t.Error("idempotency key змінено")
			}
			var b map[string]any
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				t.Error(err)
			}
			if len(b) != 2 || b["email"] != "example@example.com" || b["name"] != "Приклад" {
				t.Errorf("неочікуваний payload: %v", b)
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			var b map[string]any
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				t.Error(err)
			}
			if len(b) != 2 || b["confirm_email"] != "example@example.com" || b["reason"] != "Погоджене видалення" {
				t.Errorf("неочікуваний delete payload: %v", b)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(User{ID: testID, Email: "example@example.com", OrganizationID: "abie-ua", State: "active"})
	}))
	defer s.Close()
	c, err := New(s.URL, tokenFile(t))
	if err != nil {
		t.Fatal(err)
	}
	name := "Приклад"
	if _, err := c.Create(context.Background(), "abie-ua", "example@example.com", &name, testID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(context.Background(), "abie-ua", testID); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), "abie-ua", testID, "example@example.com", "Погоджене видалення"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "POST,GET,DELETE" {
		t.Fatal(methods)
	}
}

// TestFailureSemantics не плутає відмову доступу, timeout і authoritative absence.
func TestFailureSemantics(t *testing.T) {
	for _, code := range []int{401, 403, 404, 409, 422, 429, 500, 502, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(code)
				if code == 404 {
					_, _ = w.Write([]byte(`{"error":{"code":"identity_not_found"}}`))
					return
				}
				_, _ = w.Write([]byte("sample-secret confidential-response"))
			}))
			defer s.Close()
			c, _ := New(s.URL, tokenFile(t))
			_, err := c.Read(context.Background(), "abie-ua", testID)
			if err == nil {
				t.Fatal("очікувалася помилка")
			}
			if errors.Is(err, ErrNotFound) != (code == 404) {
				t.Fatalf("помилка класифікації: %v", err)
			}
			if strings.Contains(err.Error(), "sample-secret") || strings.Contains(err.Error(), "confidential-response") {
				t.Fatal("витік response body")
			}
			if calls != 1 {
				t.Fatal("неочікуваний retry")
			}
			deleteErr := c.Delete(context.Background(), "abie-ua", testID, "example@example.com", "Погоджене видалення")
			if (deleteErr == nil) != (code == 404) {
				t.Fatalf("delete: %v", deleteErr)
			}
		})
	}
}

// TestGeneric404PreservesState не трактує route miss чи opaque denial як відсутність.
func TestGeneric404PreservesState(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found"}}`))
	}))
	defer s.Close()
	c, _ := New(s.URL, tokenFile(t))
	if _, err := c.Read(context.Background(), "abie-ua", testID); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatal("generic 404 прийнято як absence")
	}
	if err := c.Delete(context.Background(), "abie-ua", testID, "example@example.com", "Погоджене видалення"); err == nil {
		t.Fatal("generic 404 delete прийнято")
	}
}

// TestTokenRefreshAndRedirect перечитує token і не переходить на інший endpoint.
func TestTokenRefreshAndRedirect(t *testing.T) {
	p := tokenFile(t)
	var tokens []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(User{ID: testID, Email: "example@example.com", OrganizationID: "abie-ua", State: "active"})
	}))
	defer s.Close()
	c, _ := New(s.URL, p)
	if _, err := c.Read(context.Background(), "abie-ua", testID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("sample-secret-rotated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(context.Background(), "abie-ua", testID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(tokens, ",") != "Bearer sample-secret,Bearer sample-secret-rotated" {
		t.Fatal("token не оновлюється")
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, s.URL, http.StatusTemporaryRedirect) }))
	defer redirect.Close()
	c, _ = New(redirect.URL, p)
	if _, err := c.Read(context.Background(), "abie-ua", testID); err == nil {
		t.Fatal("redirect прийнято")
	}
	if len(tokens) != 2 {
		t.Fatal("token переслано через redirect")
	}
}

// TestUnsafeInputAndResponse блокує path injection та підміну remote identity.
func TestUnsafeInputAndResponse(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?token=x", "https://example.com#fragment", "file:///tmp/test"} {
		if _, err := New(endpoint, "fixture.token"); err == nil {
			t.Errorf("прийнято endpoint %s", endpoint)
		}
	}
	for _, org := range []string{"", ".", "..", "../abie", "abie%2fua", "abie?x"} {
		if _, err := path(org, testID); err == nil {
			t.Errorf("прийнято org %q", org)
		}
	}
	for _, user := range []User{
		{ID: "invalid", Email: "e@example.com", OrganizationID: "abie-ua", State: "active"},
		{ID: testID, Email: "e@example.com", OrganizationID: "other", State: "active"},
		{ID: testID, OrganizationID: "abie-ua", State: "active"},
	} {
		if err := validateUser(user, "abie-ua", testID); err == nil {
			t.Fatal("прийнято некоректну identity")
		}
	}
}
