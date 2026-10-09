package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// TestExternalUserDocsCoverSchema компенсує static docs для змішаних prefixes.
func TestExternalUserDocsCoverSchema(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	doc, err := os.ReadFile(filepath.Join(root, "templates", "resources", "ory_external_user.md"))
	if err != nil {
		t.Fatal(err)
	}
	var resp resource.SchemaResponse
	NewExternalUserResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	for name := range resp.Schema.Attributes {
		if !strings.Contains(string(doc), "`"+name+"`") {
			t.Errorf("docs не містять %s", name)
		}
	}
}
