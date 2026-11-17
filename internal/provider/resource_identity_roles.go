package provider

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nitra/terraform-provider-ory/internal/kratosadmin"
)

type identityRolesResource struct{ client *kratosadmin.Client }
type identityRolesModel struct {
	ID    types.String `tfsdk:"id"`
	Roles types.List   `tfsdk:"roles"`
}

var _ resource.ResourceWithConfigure = &identityRolesResource{}
var _ resource.ResourceWithImportState = &identityRolesResource{}

func NewIdentityRolesResource() resource.Resource { return &identityRolesResource{} }
func (*identityRolesResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "ory_identity_roles"
}
func (*identityRolesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Authoritative ordered roles of an existing Kratos identity. Import is required; identity deletion is blocked.", Attributes: map[string]schema.Attribute{
		"id":    schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Existing Kratos identity UUID."},
		"roles": schema.ListAttribute{Required: true, ElementType: types.StringType, MarkdownDescription: "Complete ordered metadata_public.roles list. The first role can determine the default role."},
	}}
}
func (r *identityRolesResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	bundle, ok := req.ProviderData.(*apiClient)
	if !ok || bundle.kratos == nil {
		resp.Diagnostics.AddError("kratos_admin не налаштований", "Налаштуйте Kratos Admin endpoint.")
		return
	}
	r.client = bundle.kratos
}
func identityRoles(doc json.RawMessage) ([]string, bool, error) {
	var root struct {
		Metadata json.RawMessage `json:"metadata_public"`
	}
	if json.Unmarshal(doc, &root) != nil {
		return nil, false, errors.New("некоректний metadata_public")
	}
	if string(root.Metadata) == "null" {
		return nil, false, errors.New("metadata_public відсутній; потрібне окреме приймання")
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(root.Metadata, &metadata) != nil || metadata == nil {
		return nil, false, errors.New("некоректний metadata_public")
	}
	raw, exists := metadata["roles"]
	if !exists {
		return []string{}, false, nil
	}
	var roles []string
	if json.Unmarshal(raw, &roles) != nil || roles == nil {
		return nil, false, errors.New("metadata_public.roles має бути масивом рядків")
	}
	return roles, true, nil
}
func roleList(roles []string) types.List {
	values := make([]attr.Value, len(roles))
	for i, role := range roles {
		values[i] = types.StringValue(role)
	}
	result, _ := types.ListValue(types.StringType, values)
	return result
}
func (r *identityRolesResource) Create(_ context.Context, _ resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError("Потрібен import", "Імпортуйте існуючий UUID; створення identity заборонено.")
}
func (r *identityRolesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state identityRolesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	actual, err := r.client.Read(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося прочитати identity", err.Error())
		return
	}
	roles, _, err := identityRoles(actual.Document)
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося прочитати ролі", err.Error())
		return
	}
	state.Roles = roleList(roles)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *identityRolesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state identityRolesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var desired, previous []string
	resp.Diagnostics.Append(plan.Roles.ElementsAs(ctx, &desired, false)...)
	resp.Diagnostics.Append(state.Roles.ElementsAs(ctx, &previous, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if desired == nil {
		desired = []string{}
	}
	if previous == nil {
		previous = []string{}
	}
	seen := map[string]bool{}
	for _, role := range desired {
		if role == "" || seen[role] {
			resp.Diagnostics.AddError("Некоректні ролі", "Ролі мають бути непорожні та унікальні.")
			return
		}
		seen[role] = true
	}
	current, err := r.client.Read(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося перечитати identity", err.Error())
		return
	}
	live, _, err := identityRoles(current.Document)
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося прочитати ролі", err.Error())
		return
	}
	if !reflect.DeepEqual(live, previous) && !reflect.DeepEqual(live, desired) {
		resp.Diagnostics.AddError("Конфлікт ролей", "Ролі змінились після plan; створіть новий plan.")
		return
	}
	if !reflect.DeepEqual(live, desired) {
		_, err = r.client.PatchWithCurrent(ctx, state.ID.ValueString(), []kratosadmin.Operation{{Op: "add", Path: "/metadata_public/roles", Value: desired}}, current)
		if err != nil {
			resp.Diagnostics.AddError("Не вдалося оновити ролі", err.Error())
			return
		}
	}
	actual, err := r.client.Read(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося перевірити ролі", err.Error())
		return
	}
	confirmed, _, err := identityRoles(actual.Document)
	if err != nil || !reflect.DeepEqual(confirmed, desired) {
		resp.Diagnostics.AddError("Kratos не підтвердив ролі", "Перечитайте identity перед повторним plan.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (*identityRolesResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddError("Видалення заблоковано", "Для припинення керування використайте removed із destroy = false; identity і ролі залишаться незмінними.")
}
func (r *identityRolesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := uuid.Parse(req.ID)
	if err != nil || id.String() != req.ID {
		resp.Diagnostics.AddError("Некоректний UUID", "Потрібен canonical UUID identity.")
		return
	}
	actual, err := r.client.Read(ctx, id.String())
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося імпортувати identity", err.Error())
		return
	}
	roles, _, err := identityRoles(actual.Document)
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося імпортувати ролі", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &identityRolesModel{ID: types.StringValue(id.String()), Roles: roleList(roles)})...)
}
