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
	"github.com/nitra/terraform-provider-ory/internal/kratosadmin"
)

type platformIdentityResource struct{ client *kratosadmin.Client }
type platformIdentityModel struct {
	ID       types.String         `tfsdk:"id"`
	Document jsontypes.Normalized `tfsdk:"document"`
	Observed jsontypes.Normalized `tfsdk:"observed_document"`
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
		"id":                schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Наявний UUID; імпортуйте identity перед керуванням."},
		"document":          schema.StringAttribute{Required: true, Sensitive: true, CustomType: jsontypes.NormalizedType{}, MarkdownDescription: "Останній бажаний JSON. Зовнішні зміни до інших JSON-шляхів зберігаються; конфлікт на зміненому шляху зупиняє apply."},
		"observed_document": schema.StringAttribute{Computed: true, Sensitive: true, CustomType: jsontypes.NormalizedType{}, MarkdownDescription: "Останній прочитаний стан Kratos; може відрізнятися від document після зовнішніх змін."},
		"revision":          schema.StringAttribute{Computed: true, MarkdownDescription: "Fingerprint поточного документа та updated_at для перевірки перед записом."},
	}}
}
func (r *platformIdentityResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	bundle, ok := req.ProviderData.(*apiClient)
	if !ok || bundle.kratos == nil {
		resp.Diagnostics.AddError("kratos_admin не налаштований", "Додайте Kratos Admin endpoint через provider kratos_admin або KRATOS_ADMIN_URL.")
		return
	}
	r.client = bundle.kratos
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
	if err := kratosadmin.ValidateDocument([]byte(plan.Document.ValueString())); err != nil {
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
	actual, err := r.client.Read(ctx, state.ID.ValueString())
	if errors.Is(err, kratosadmin.ErrNotFound) {
		// Keep state: absent imported identities must never be silently recreated.
		resp.Diagnostics.AddError("Імпортована identity відсутня", "Перевірте Kratos та recovery; state збережено, recreate заблоковано.")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося прочитати identity", err.Error())
		return
	}
	// Keep the previous desired document as the intent baseline. Only the
	// observed document follows external changes during refresh.
	state.Observed = jsontypes.NewNormalizedValue(string(actual.Document))
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
	current, err := r.client.Read(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося перечитати identity", err.Error())
		return
	}
	changes, err := kratosadmin.Merge(json.RawMessage(state.Document.ValueString()), json.RawMessage(plan.Document.ValueString()), current.Document)
	if err != nil {
		resp.Diagnostics.AddError("Конфлікт identity", err.Error())
		return
	}
	actual := current
	if len(changes) > 0 {
		actual, err = r.client.PatchWithCurrent(ctx, state.ID.ValueString(), changes, current)
		if err == nil {
			actual, err = r.client.Read(ctx, state.ID.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося оновити identity", err.Error())
		return
	}
	remaining, err := kratosadmin.Merge(json.RawMessage(state.Document.ValueString()), json.RawMessage(plan.Document.ValueString()), actual.Document)
	if err != nil || len(remaining) > 0 {
		resp.Diagnostics.AddError("Kratos не підтвердив бажану зміну", "Перечитайте identity перед повторним plan; результат PATCH може бути неоднозначним.")
		return
	}
	plan.Observed = jsontypes.NewNormalizedValue(string(actual.Document))
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
	actual, err := r.client.Read(ctx, id.String())
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося імпортувати identity", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &platformIdentityModel{ID: types.StringValue(actual.ID), Document: jsontypes.NewNormalizedValue(string(actual.Document)), Observed: jsontypes.NewNormalizedValue(string(actual.Document)), Revision: types.StringValue(actual.Revision)})...)
}
