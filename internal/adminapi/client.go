// Package adminapi реалізує вузький HTTP-контракт захищеного execution API.
package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("identity не існує")

// Client не зберігає credentials у resource state і не пересилає їх при redirect.
type Client struct {
	base      string
	tokenFile string
	http      *http.Client
}

// User містить тільки дозволені поля, без credentials і довільних metadata.
type User struct {
	ID             string  `json:"id"`
	Email          string  `json:"email"`
	Name           *string `json:"name"`
	State          string  `json:"state"`
	OrganizationID string  `json:"organization_id"`
}

// New перевіряє URL; HTTP дозволений виключно для loopback тестів.
func New(endpoint, tokenFile string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("endpoint має бути абсолютним HTTPS URL без credentials, query і fragment")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return nil, errors.New("HTTP дозволений тільки для loopback")
	}
	if tokenFile == "" {
		return nil, errors.New("потрібен шлях до файла bearer token")
	}
	return &Client{base: strings.TrimRight(endpoint, "/"), tokenFile: tokenFile, http: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// path валідує сегменти перед побудовою URL.
func path(org, id string) (string, error) {
	if org == "" || strings.TrimSpace(org) != org || org == "." || org == ".." || strings.ContainsAny(org, "/\\?#%") {
		return "", errors.New("некоректний organization_id")
	}
	p := "/v1/organizations/" + url.PathEscape(org) + "/users"
	if id != "" {
		if _, err := uuid.Parse(id); err != nil {
			return "", errors.New("identity_id має бути UUID")
		}
		p += "/" + id
	}
	return p, nil
}

// Create надсилає тільки email/name; тип і права визначає сервер.
func (c *Client) Create(ctx context.Context, org, email string, name *string, key string) (User, error) {
	var user User
	p, err := path(org, "")
	if err != nil {
		return user, err
	}
	if _, err := uuid.Parse(key); err != nil {
		return user, errors.New("idempotency key має бути UUID")
	}
	body := struct {
		Email string  `json:"email"`
		Name  *string `json:"name,omitempty"`
	}{email, name}
	err = c.request(ctx, http.MethodPost, p, body, key, http.StatusCreated, &user)
	if err == nil {
		err = validateUser(user, org, "")
	}
	return user, err
}

// Read відрізняє справжній 404 від відмови в доступі чи помилки мережі.
func (c *Client) Read(ctx context.Context, org, id string) (User, error) {
	var user User
	p, err := path(org, id)
	if err != nil {
		return user, err
	}
	err = c.request(ctx, http.MethodGet, p, nil, "", http.StatusOK, &user)
	if err == nil {
		err = validateUser(user, org, id)
	}
	return user, err
}

// Delete підтверджує email зі state; 404 означає, що видалення вже завершене.
func (c *Client) Delete(ctx context.Context, org, id, email, reason string) error {
	p, err := path(org, id)
	if err != nil {
		return err
	}
	body := struct {
		Email  string `json:"confirm_email"`
		Reason string `json:"reason"`
	}{email, reason}
	err = c.request(ctx, http.MethodDelete, p, body, "", http.StatusNoContent, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// validateUser не дозволяє помилковій відповіді підмінити identity чи організацію.
func validateUser(user User, org, id string) error {
	if _, err := uuid.Parse(user.ID); err != nil || (id != "" && user.ID != id) || user.OrganizationID != org || user.Email == "" || user.State == "" {
		return errors.New("execution API повернув некоректну identity або іншу організацію")
	}
	return nil
}

// request перечитує token перед кожним викликом; не логує bearer чи response body.
func (c *Client) request(ctx context.Context, method, path string, body any, key string, expected int, out any) error {
	tokenBytes, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return errors.New("не вдалося прочитати файл bearer token")
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return errors.New("файл bearer token порожній або некоректний")
	}
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return errors.New("не вдалося серіалізувати запит")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return errors.New("не вдалося створити HTTP-запит")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("execution API недоступний; результат mutation може бути невідомим, перевірте identity перед повторним plan")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && method != http.MethodPost {
		// Generic 404 може бути route miss чи прихованою відмовою в доступі.
		// State можна прибирати тільки за явним authoritative кодом execution API.
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&envelope) == nil && envelope.Error.Code == "identity_not_found" {
			return ErrNotFound
		}
		return errors.New("execution API: неавторитетний HTTP 404; identity залишена у state")
	}
	if resp.StatusCode != expected {
		return fmt.Errorf("execution API: HTTP %d; response body прихований", resp.StatusCode)
	}
	if out != nil {
		data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		if err != nil || len(data) > 1<<20 {
			return errors.New("некоректна або завелика відповідь execution API")
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("execution API повернув некоректний JSON")
		}
	}
	return nil
}
