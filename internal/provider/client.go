package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/nitra/terraform-provider-ory/internal/adminapi"
	hydra "github.com/ory/hydra-client-go/v2"
)

// apiClient is shared by all resources and data sources (provider data).
type apiClient struct {
	users    *adminapi.Client
	hydra    *hydra.APIClient
	http     *http.Client
	endpoint string // Admin API base URL without trailing slash
	retry    *retryPolicy
	ua       string
}

type retryPolicy struct {
	maxElapsed    time.Duration
	maxInterval   time.Duration
	randomization float64
}

// do runs fn and, when a retry policy is configured, retries it while Hydra
// answers 429 Too Many Requests. Any other error is returned immediately.
func (c *apiClient) do(ctx context.Context, fn func() (*http.Response, error)) (*http.Response, error) {
	if c.retry == nil {
		return fn()
	}
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = c.retry.maxElapsed
	b.MaxInterval = c.retry.maxInterval
	b.RandomizationFactor = c.retry.randomization

	var resp *http.Response
	err := backoff.Retry(func() error {
		var err error
		resp, err = fn()
		if err != nil && resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			return err
		}
		if err != nil {
			return backoff.Permanent(err)
		}
		return nil
	}, backoff.WithContext(b, ctx))
	return resp, err
}

// rawJSON performs a JSON request against the Admin API with the configured
// (authenticated) HTTP client. It is used where the generated client's strict
// request models are not suitable (see internal/httpclient/README-VENDOR.md).
func (c *apiClient) rawJSON(ctx context.Context, method, p string, body any, out any) (*http.Response, error) {
	return c.do(ctx, func() (*http.Response, error) {
		var rdr io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			rdr = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.endpoint+p, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.ua != "" {
			req.Header.Set("User-Agent", c.ua)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return resp, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp, err
		}
		if resp.StatusCode >= 300 {
			return resp, &httpError{status: resp.StatusCode, body: data}
		}
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return resp, fmt.Errorf("decoding %s %s response: %w", method, p, err)
			}
		}
		return resp, nil
	})
}

type httpError struct {
	status int
	body   []byte
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.status, bytes.TrimSpace(e.body))
}

// statusOf returns the HTTP status code of resp, or 0.
func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// apiErrorDetail renders err including the Hydra error body when available.
func apiErrorDetail(err error) string {
	var ge *hydra.GenericOpenAPIError
	if errors.As(err, &ge) && len(ge.Body()) > 0 {
		return fmt.Sprintf("%s: %s", ge.Error(), bytes.TrimSpace(ge.Body()))
	}
	return err.Error()
}

func addAPIError(d *diag.Diagnostics, summary string, err error) {
	d.AddError(summary, apiErrorDetail(err))
}

func clientFromProviderData(data any, d *diag.Diagnostics) *apiClient {
	if data == nil {
		return nil
	}
	c, ok := data.(*apiClient)
	if !ok {
		d.AddError("Unexpected provider data", fmt.Sprintf("expected *apiClient, got %T", data))
		return nil
	}
	if c.hydra == nil {
		d.AddError("Hydra endpoint не налаштований", "Для hydra_* resources потрібен endpoint або HYDRA_ADMIN_URL.")
		return nil
	}
	return c
}
