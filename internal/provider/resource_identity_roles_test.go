package provider

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestIdentityRolesParsing(t *testing.T) {
	tests := []struct {
		name, input  string
		want         []string
		exists, fail bool
	}{
		{"ordered", `{"metadata_public":{"roles":["gt","abie:dagster"],"other":"keep"}}`, []string{"gt", "abie:dagster"}, true, false},
		{"missing roles", `{"metadata_public":{"other":"keep"}}`, []string{}, false, false},
		{"null metadata", `{"metadata_public":null}`, nil, false, true},
		{"null roles", `{"metadata_public":{"roles":null}}`, nil, false, true},
		{"invalid roles", `{"metadata_public":{"roles":[1]}}`, nil, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, exists, err := identityRoles(json.RawMessage(tt.input))
			if (err != nil) != tt.fail || exists != tt.exists || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, %v, %v", got, exists, err)
			}
		})
	}
}
