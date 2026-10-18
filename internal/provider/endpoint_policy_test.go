package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestConfigureKratosRemoteAllowlist(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HYDRA_ADMIN_URL", "")
	t.Setenv("KRATOS_ADMIN_URL", "")
	// A similarly named environment variable must never opt in to remote access.
	t.Setenv("KRATOS_ADMIN_ALLOWED_REMOTE_ENDPOINTS", "http://platform-identities-read:8080")
	allowed := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("http://platform-identities-read:8080")})
	for _, tc := range []struct {
		name, endpoint string
		allow          types.Set
		wantError      bool
	}{
		{"default loopback", "http://127.0.0.1:8080", types.SetNull(types.StringType), false},
		{"default remote denied", "http://platform-identities-read:8080", types.SetNull(types.StringType), true},
		{"exact remote opt-in", "http://platform-identities-read:8080", allowed, false},
		{"port mismatch", "http://platform-identities-read:8081", allowed, true},
		{"missing endpoint", "", allowed, true},
		{"unknown allowlist", "http://127.0.0.1:8080", types.SetUnknown(types.StringType), true},
		{"unknown element", "http://127.0.0.1:8080", types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}), true},
		{"null element", "http://127.0.0.1:8080", types.SetValueMust(types.StringType, []attr.Value{types.StringNull()}), true},
		{"invalid unused entry", "http://127.0.0.1:8080", types.SetValueMust(types.StringType, []attr.Value{types.StringValue("http://*.svc")}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New("test")()
			var schema fwprovider.SchemaResponse
			p.Schema(ctx, fwprovider.SchemaRequest{}, &schema)
			state := tfsdk.State{Schema: schema.Schema}
			model := providerModel{KratosAdmin: types.StringValue(tc.endpoint), KratosAdminAllowedRemoteEndpoints: tc.allow, Endpoint: types.StringNull()}
			if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			var response fwprovider.ConfigureResponse
			p.Configure(ctx, fwprovider.ConfigureRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: state.Raw}}, &response)
			if response.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("unexpected configure result: %v", response.Diagnostics)
			}
			if !tc.wantError && response.ResourceData == nil {
				t.Fatal("no configured Kratos client")
			}
		})
	}
}
