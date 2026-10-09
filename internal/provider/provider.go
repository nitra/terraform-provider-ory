// Package provider implements the nitra/ory OpenTofu/Terraform provider for
// self-hosted Ory Hydra (Admin API) on terraform-plugin-framework.
//
// Originally derived from github.com/svrakitin/terraform-provider-hydra
// (MIT, Copyright (c) 2021 Stepan Rakitin); rewritten from SDKv2 to the
// Plugin Framework.
package provider

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nitra/terraform-provider-ory/internal/adminapi"
	hydra "github.com/ory/hydra-client-go/v2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

var _ provider.Provider = (*hydraProvider)(nil)

// New returns a provider factory for providerserver.Serve and acceptance tests.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &hydraProvider{version: version}
	}
}

type hydraProvider struct {
	version string
}

type providerModel struct {
	UserAPI        *userAPIModel        `tfsdk:"user_api"`
	Endpoint       types.String         `tfsdk:"endpoint"`
	RetryPolicy    *retryPolicyModel    `tfsdk:"retry_policy"`
	Authentication *authenticationModel `tfsdk:"authentication"`
}

// userAPIModel відокремлює user execution credentials від Hydra Admin API.
type userAPIModel struct {
	Endpoint  types.String `tfsdk:"endpoint"`
	TokenFile types.String `tfsdk:"token_file"`
}

type retryPolicyModel struct {
	Enabled             types.Bool    `tfsdk:"enabled"`
	MaxElapsedTime      types.String  `tfsdk:"max_elapsed_time"`
	MaxInterval         types.String  `tfsdk:"max_interval"`
	RandomizationFactor types.Float64 `tfsdk:"randomization_factor"`
}

type authenticationModel struct {
	Basic      *basicAuthModel      `tfsdk:"basic"`
	HTTPHeader *httpHeaderAuthModel `tfsdk:"http_header"`
	OAuth2     *oauth2AuthModel     `tfsdk:"oauth2"`
	TLS        *tlsAuthModel        `tfsdk:"tls"`
}

type basicAuthModel struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

type httpHeaderAuthModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
}

type oauth2AuthModel struct {
	TokenEndpoint types.String `tfsdk:"token_endpoint"`
	ClientID      types.String `tfsdk:"client_id"`
	ClientSecret  types.String `tfsdk:"client_secret"`
	Audience      types.List   `tfsdk:"audience"`
	Scopes        types.List   `tfsdk:"scopes"`
}

type tlsAuthModel struct {
	InsecureSkipVerify types.Bool   `tfsdk:"insecure_skip_verify"`
	Certificate        types.String `tfsdk:"certificate"`
	Key                types.String `tfsdk:"key"`
}

func (p *hydraProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "hydra"
	resp.Version = p.version
}

func (p *hydraProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	envAttr := func(desc, env string, sensitive bool) schema.StringAttribute {
		return schema.StringAttribute{
			Optional:            true,
			Sensitive:           sensitive,
			MarkdownDescription: fmt.Sprintf("%s Can also be set with the `%s` environment variable.", desc, env),
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages self-hosted [Ory Hydra](https://github.com/ory/hydra) (tested against `oryd/hydra:v26.2.0`) through its Admin API.",
		Attributes: map[string]schema.Attribute{
			"endpoint": envAttr("Hydra Admin API base URL, e.g. `http://hydra-admin:4445`.", "HYDRA_ADMIN_URL", false),
		},
		Blocks: map[string]schema.Block{
			"user_api": schema.SingleNestedBlock{
				MarkdownDescription: "Захищений execution API external користувачів. Не Kratos Admin API; endpoint і token file незалежні від Hydra credentials.",
				Attributes: map[string]schema.Attribute{
					"endpoint":   schema.StringAttribute{Optional: true, MarkdownDescription: "HTTPS URL або ORY_ADMIN_ENDPOINT. HTTP тільки для loopback tests."},
					"token_file": schema.StringAttribute{Optional: true, MarkdownDescription: "Шлях до bearer token file або ORY_ADMIN_TOKEN_FILE. Вміст перечитується перед кожним запитом й не входить у HCL/state."},
				},
			},
			"retry_policy": schema.SingleNestedBlock{
				MarkdownDescription: "Retry API requests that were throttled (HTTP 429) with exponential back-off.",
				Attributes: map[string]schema.Attribute{
					"enabled":              schema.BoolAttribute{Optional: true, MarkdownDescription: "Enable retries. Defaults to `false`."},
					"max_elapsed_time":     schema.StringAttribute{Optional: true, MarkdownDescription: "Maximum time spent retrying. Defaults to `30s`."},
					"max_interval":         schema.StringAttribute{Optional: true, MarkdownDescription: "Maximum interval between retries. Defaults to `3s`."},
					"randomization_factor": schema.Float64Attribute{Optional: true, MarkdownDescription: "Jitter factor. Defaults to `0.5`."},
				},
			},
			"authentication": schema.SingleNestedBlock{
				MarkdownDescription: "How to authenticate against the Admin API (it is usually only reachable in-cluster and unauthenticated).",
				Blocks: map[string]schema.Block{
					"basic": schema.SingleNestedBlock{
						Attributes: map[string]schema.Attribute{
							"username": envAttr("Basic auth user name.", "HYDRA_ADMIN_BASIC_AUTH_USERNAME", false),
							"password": envAttr("Basic auth password.", "HYDRA_ADMIN_BASIC_AUTH_PASSWORD", true),
						},
					},
					"http_header": schema.SingleNestedBlock{
						Attributes: map[string]schema.Attribute{
							"name":  envAttr("Header name, defaults to `Authorization`.", "HYDRA_ADMIN_AUTH_HTTP_HEADER_NAME", false),
							"value": envAttr("Header value.", "HYDRA_ADMIN_AUTH_HTTP_HEADER_VALUE", true),
						},
					},
					"oauth2": schema.SingleNestedBlock{
						Attributes: map[string]schema.Attribute{
							"token_endpoint": envAttr("Token endpoint used to obtain an access token (client credentials).", "HYDRA_ADMIN_OAUTH2_TOKEN_ENDPOINT", false),
							"client_id":      envAttr("Client ID.", "HYDRA_ADMIN_OAUTH2_CLIENT_ID", false),
							"client_secret":  envAttr("Client secret.", "HYDRA_ADMIN_OAUTH2_CLIENT_SECRET", true),
							"audience":       schema.ListAttribute{ElementType: types.StringType, Optional: true, MarkdownDescription: "Requested audience."},
							"scopes":         schema.ListAttribute{ElementType: types.StringType, Optional: true, MarkdownDescription: "Requested scopes."},
						},
					},
					"tls": schema.SingleNestedBlock{
						Attributes: map[string]schema.Attribute{
							"insecure_skip_verify": schema.BoolAttribute{Optional: true, MarkdownDescription: "Skip server certificate verification."},
							"certificate":          envAttr("PEM client certificate.", "HYDRA_ADMIN_TLS_AUTH_CERT_DATA", true),
							"key":                  envAttr("PEM client key.", "HYDRA_ADMIN_TLS_AUTH_KEY_DATA", true),
						},
					},
				},
			},
		},
	}
}

func strOrEnv(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func (p *hydraProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var users *adminapi.Client
	if cfg.UserAPI != nil {
		if cfg.UserAPI.Endpoint.IsUnknown() || cfg.UserAPI.TokenFile.IsUnknown() {
			resp.Diagnostics.AddError("Невідома user_api конфігурація", "endpoint і token_file мають бути відомими під час plan.")
			return
		}
		var err error
		users, err = adminapi.New(strOrEnv(cfg.UserAPI.Endpoint, "ORY_ADMIN_ENDPOINT"), strOrEnv(cfg.UserAPI.TokenFile, "ORY_ADMIN_TOKEN_FILE"))
		if err != nil {
			resp.Diagnostics.AddError("Некоректний user_api", err.Error())
			return
		}
	}
	endpoint := strOrEnv(cfg.Endpoint, "HYDRA_ADMIN_URL")
	if endpoint == "" {
		if users != nil {
			resp.ResourceData = &apiClient{users: users}
			resp.DataSourceData = &apiClient{users: users}
			return
		}
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Missing Hydra Admin endpoint",
			"Set `endpoint` in the provider block or the HYDRA_ADMIN_URL environment variable.")
		return
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Invalid Hydra Admin endpoint", fmt.Sprintf("%q is not an absolute URL", endpoint))
		return
	}

	httpClient, err := buildHTTPClient(ctx, cfg.Authentication)
	if err != nil {
		resp.Diagnostics.AddError("Invalid authentication configuration", err.Error())
		return
	}

	retry, err := buildRetryPolicy(cfg.RetryPolicy)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("retry_policy"), "Invalid retry_policy", err.Error())
		return
	}

	hc := hydra.NewConfiguration()
	hc.HTTPClient = httpClient
	hc.UserAgent = "terraform-provider-ory/" + p.version
	base := strings.TrimRight(u.String(), "/")
	hc.Servers = hydra.ServerConfigurations{{URL: base}}

	c := &apiClient{
		users:    users,
		hydra:    hydra.NewAPIClient(hc),
		http:     httpClient,
		endpoint: base,
		retry:    retry,
		ua:       hc.UserAgent,
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func buildRetryPolicy(m *retryPolicyModel) (*retryPolicy, error) {
	if m == nil || m.Enabled.IsNull() || !m.Enabled.ValueBool() {
		return nil, nil
	}
	rp := &retryPolicy{maxElapsed: 30 * time.Second, maxInterval: 3 * time.Second, randomization: 0.5}
	if v := m.MaxElapsedTime.ValueString(); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("max_elapsed_time: %w", err)
		}
		rp.maxElapsed = d
	}
	if v := m.MaxInterval.ValueString(); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("max_interval: %w", err)
		}
		rp.maxInterval = d
	}
	if !m.RandomizationFactor.IsNull() {
		rp.randomization = m.RandomizationFactor.ValueFloat64()
	}
	return rp, nil
}

func buildHTTPClient(ctx context.Context, auth *authenticationModel) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second}
	if auth == nil {
		return client, nil
	}

	if auth.TLS != nil {
		cert := strOrEnv(auth.TLS.Certificate, "HYDRA_ADMIN_TLS_AUTH_CERT_DATA")
		key := strOrEnv(auth.TLS.Key, "HYDRA_ADMIN_TLS_AUTH_KEY_DATA")
		kp, err := tls.X509KeyPair([]byte(cert), []byte(key))
		if err != nil {
			return nil, fmt.Errorf("tls: %w", err)
		}
		transport.TLSClientConfig = &tls.Config{
			Certificates:       []tls.Certificate{kp},
			InsecureSkipVerify: auth.TLS.InsecureSkipVerify.ValueBool(), //nolint:gosec // explicit user opt-in
			MinVersion:         tls.VersionTLS12,
		}
	}

	var rt http.RoundTripper = transport
	switch {
	case auth.Basic != nil:
		user := strOrEnv(auth.Basic.Username, "HYDRA_ADMIN_BASIC_AUTH_USERNAME")
		pass := strOrEnv(auth.Basic.Password, "HYDRA_ADMIN_BASIC_AUTH_PASSWORD")
		if user == "" {
			return nil, fmt.Errorf("basic: username is required")
		}
		rt = &headerTransport{next: rt, set: func(r *http.Request) { r.SetBasicAuth(user, pass) }}
	case auth.HTTPHeader != nil:
		name := strOrEnv(auth.HTTPHeader.Name, "HYDRA_ADMIN_AUTH_HTTP_HEADER_NAME")
		if name == "" {
			name = "Authorization"
		}
		value := strOrEnv(auth.HTTPHeader.Value, "HYDRA_ADMIN_AUTH_HTTP_HEADER_VALUE")
		rt = &headerTransport{next: rt, set: func(r *http.Request) { r.Header.Set(name, value) }}
	case auth.OAuth2 != nil:
		cc := clientcredentials.Config{
			TokenURL:       strOrEnv(auth.OAuth2.TokenEndpoint, "HYDRA_ADMIN_OAUTH2_TOKEN_ENDPOINT"),
			ClientID:       strOrEnv(auth.OAuth2.ClientID, "HYDRA_ADMIN_OAUTH2_CLIENT_ID"),
			ClientSecret:   strOrEnv(auth.OAuth2.ClientSecret, "HYDRA_ADMIN_OAUTH2_CLIENT_SECRET"),
			EndpointParams: url.Values{},
		}
		var scopes, aud []string
		if !auth.OAuth2.Scopes.IsNull() {
			_ = auth.OAuth2.Scopes.ElementsAs(ctx, &scopes, false)
		}
		if !auth.OAuth2.Audience.IsNull() {
			_ = auth.OAuth2.Audience.ElementsAs(ctx, &aud, false)
		}
		cc.Scopes = scopes
		if len(aud) > 0 {
			cc.EndpointParams.Set("audience", strings.Join(aud, " "))
		}
		tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: transport})
		rt = &oauth2.Transport{Base: transport, Source: oauth2.ReuseTokenSource(nil, cc.TokenSource(tokenCtx))}
	}
	client.Transport = rt
	return client, nil
}

type headerTransport struct {
	next http.RoundTripper
	set  func(*http.Request)
}

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	t.set(r)
	return t.next.RoundTrip(r)
}

func (p *hydraProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewExternalUserResource,
		NewOAuth2ClientResource,
		NewTrustedJWTGrantIssuerResource,
	}
}

func (p *hydraProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewTrustedJWTGrantIssuersDataSource,
	}
}
