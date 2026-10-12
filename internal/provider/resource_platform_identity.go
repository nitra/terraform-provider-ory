package provider

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nitra/terraform-provider-ory/internal/adminapi"
)

type platformIdentityResource struct{ client *adminapi.Client }
type platformIdentityModel struct {
	ID       types.String         `tfsdk:"id"`
	Document jsontypes.Normalized `tfsdk:"document"`
	Revision types.String         `tfsdk:"revision"`
}

var _ resource.ResourceWithImportState = &platformIdentityResource{}
var _ resource.ResourceWithConfigure = &platformIdentityResource{}
var _ resource.ResourceWithModifyPlan = &platformIdentityResource{}

func NewPlatformIdentityResource() resource.Resource { return &platformIdentityResource{} }
func (r *platformIdentityResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "ory_platform_identity"
}
func (r *platformIdentityResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Імпорт і редагування усіх керованих полів наявної Kratos identity зі збереженням UUID та credentials. Create/recreate/delete заблоковані у фазі adoption.", Attributes: map[string]schema.Attribute{
		"id":       schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Наявний UUID; імпортуйте identity перед керуванням."},
		"document": schema.StringAttribute{Required: true, Sensitive: true, CustomType: jsontypes.NormalizedType{}, MarkdownDescription: "Повний configurable JSON: schema_id, state, traits, metadata_public, metadata_admin, verifiable_addresses, recovery_addresses, external_id, organization_id. Credentials та server-owned поля заборонені."},
		"revision": schema.StringAttribute{Computed: true, MarkdownDescription: "Fingerprint поточного документа та updated_at для перевірки перед записом."},
	}}
}
func (r *platformIdentityResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	bundle, ok := req.ProviderData.(*apiClient)
	if !ok || bundle.platform == nil {
		resp.Diagnostics.AddError("platform_api не налаштований", "Додайте окремий platform_api з Google ID token file.")
		return
	}
	r.client = bundle.platform
}
func (r *platformIdentityResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan platformIdentityModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Document.IsUnknown() || plan.Document.IsNull() {
		return
	}
	if err := adminapi.ValidatePlatformDocument([]byte(plan.Document.ValueString())); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("document"), "Некоректний identity document", err.Error())
	}
}
func (r *platformIdentityResource) Create(_ context.Context, _ resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError("Потрібен import наявної identity", "Використайте import block з UUID. Автоматичне створення чи recreate після втрати state заборонені.")
}
func (r *platformIdentityResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state platformIdentityModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	actual, err := r.client.ReadPlatform(ctx, state.ID.ValueString())
	if errors.Is(err, adminapi.ErrNotFound) {
		// Keep state: absent imported identities must never be silently recreated.
		resp.Diagnostics.AddError("Імпортована identity відсутня", "Перевірте Kratos та recovery; state збережено, recreate заблоковано.")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося прочитати identity", err.Error())
		return
	}
	state.Document = jsontypes.NewNormalizedValue(string(actual.Document))
	state.Revision = types.StringValue(actual.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *platformIdentityResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state platformIdentityModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.ID.Equal(state.ID) {
		resp.Diagnostics.AddError("Зміна UUID заборонена", "Зберігайте наявний UUID identity.")
		return
	}
	actual, err := r.client.UpdatePlatform(ctx, state.ID.ValueString(), json.RawMessage(plan.Document.ValueString()), state.Revision.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося оновити identity", err.Error())
		return
	}
	plan.Document = jsontypes.NewNormalizedValue(string(actual.Document))
	plan.Revision = types.StringValue(actual.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *platformIdentityResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddError("Видалення identity заблоковане", "Adoption resource не видаляє користувачів. Для припинення керування використайте removed block з lifecycle { destroy = false }.")
}
func (r *platformIdentityResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := uuid.Parse(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Некоректний import ID", "Потрібен наявний UUID Kratos identity.")
		return
	}
	actual, err := r.client.ReadPlatform(ctx, id.String())
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося імпортувати identity", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &platformIdentityModel{ID: types.StringValue(actual.ID), Document: jsontypes.NewNormalizedValue(string(actual.Document)), Revision: types.StringValue(actual.Revision)})...)
}
