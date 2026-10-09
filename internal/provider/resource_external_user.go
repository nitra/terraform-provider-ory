package provider

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nitra/terraform-provider-ory/internal/adminapi"
)

const defaultDeletionReason = "Видалення через OpenTofu"

type externalUserResource struct{ client *adminapi.Client }
type externalUserModel struct {
	ID              types.String `tfsdk:"id"`
	OrganizationID  types.String `tfsdk:"organization_id"`
	Email           types.String `tfsdk:"email"`
	Name            types.String `tfsdk:"name"`
	State           types.String `tfsdk:"state"`
	CreateRequestID types.String `tfsdk:"create_request_id"`
	DeletionReason  types.String `tfsdk:"deletion_reason"`
}

var (
	_ resource.Resource                = &externalUserResource{}
	_ resource.ResourceWithConfigure   = &externalUserResource{}
	_ resource.ResourceWithImportState = &externalUserResource{}
	_ resource.ResourceWithModifyPlan  = &externalUserResource{}
)

// NewExternalUserResource створює resource без доступу до довільних metadata/roles.
func NewExternalUserResource() resource.Resource { return &externalUserResource{} }

// Metadata задає ім’я resource.
func (r *externalUserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "ory_external_user"
}

// Schema описує create/delete контракт; зміни identity не запускають replacement.
func (r *externalUserResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	stable := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{MarkdownDescription: "External member однієї організації. Зміна email, name чи organization_id заборонена; автоматичний replacement не виконується.", Attributes: map[string]schema.Attribute{
		"id":                schema.StringAttribute{Computed: true, PlanModifiers: stable, MarkdownDescription: "Стабільний UUID Kratos identity."},
		"organization_id":   schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`), "потрібен organization slug")}, MarkdownDescription: "Організація, дозволена серверною policy."},
		"email":             schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(3, 254), stringvalidator.RegexMatches(regexp.MustCompile(`^[^\s@A-Z]+@[^\s@A-Z]+\.[^\s@A-Z]+$`), "email має бути у нижньому регістрі без пробілів")}, MarkdownDescription: "Email у канонічній формі, без пробілів і великих літер."},
		"name":              schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 120)}, MarkdownDescription: "Необов’язкове ім’я; без пробілів на початку та в кінці."},
		"state":             schema.StringAttribute{Computed: true, PlanModifiers: stable, MarkdownDescription: "Поточний стан identity; provider його не змінює."},
		"create_request_id": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`), "потрібен UUID запиту")}, MarkdownDescription: "Стабільний UUID idempotency key. Згенеруйте один раз для нового create request та зафіксуйте в PR; не використовуйте uuid() чи timestamp() в HCL."},
		"deletion_reason":   schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(defaultDeletionReason), Validators: []validator.String{stringvalidator.LengthBetween(8, 500)}, MarkdownDescription: "Причина видалення, збережена у state. Перед видаленням оновіть її окремим apply; після вилучення resource використовується останнє збережене значення."},
	}}
}

// Configure приймає лише наш вузький HTTP client.
func (r *externalUserResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	bundle, ok := req.ProviderData.(*apiClient)
	if !ok || bundle.users == nil {
		resp.Diagnostics.AddError("Execution API не налаштований", "Додайте user_api з endpoint і token_file.")
		return
	}
	c := bundle.users
	r.client = c
}

// ModifyPlan блокує update identity, включно з відновленням drift через replacement.
func (r *externalUserResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan externalUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Email.IsUnknown() && !plan.Email.IsNull() && (strings.TrimSpace(plan.Email.ValueString()) != plan.Email.ValueString() || strings.ToLower(plan.Email.ValueString()) != plan.Email.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("email"), "Неканонічний email", "Email має бути у нижньому регістрі без пробілів на краях.")
	}
	for key, value := range map[string]types.String{"name": plan.Name, "deletion_reason": plan.DeletionReason} {
		if !value.IsNull() && !value.IsUnknown() && (strings.TrimSpace(value.ValueString()) != value.ValueString() || strings.IndexFunc(value.ValueString(), unicode.IsControl) >= 0) {
			resp.Diagnostics.AddAttributeError(path.Root(key), "Некоректний текст", "Пробіли на краях і керівні символи заборонені.")
		}
	}
	if req.State.Raw.IsNull() {
		return
	}
	var state externalUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for key, pair := range map[string][2]types.String{"organization_id": {state.OrganizationID, plan.OrganizationID}, "email": {state.Email, plan.Email}, "name": {state.Name, plan.Name}, "create_request_id": {state.CreateRequestID, plan.CreateRequestID}} {
		if !pair[0].Equal(pair[1]) {
			resp.Diagnostics.AddAttributeError(path.Root(key), "Зміна identity заборонена", "Provider підтримує create/delete, не update чи replacement. Перевірте drift; не видаляйте identity для зміни цього поля.")
		}
	}
}

// Create зберігає отриманий UUID одразу, без другого мережевого виклику.
func (r *externalUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan externalUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var name *string
	if !plan.Name.IsNull() {
		v := plan.Name.ValueString()
		name = &v
	}
	user, err := r.client.Create(ctx, plan.OrganizationID.ValueString(), plan.Email.ValueString(), name, plan.CreateRequestID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося створити користувача", err.Error())
		return
	}
	plan.ID = types.StringValue(user.ID)
	plan.State = types.StringValue(user.State)
	// UUID записуємо навіть при порушенні response контракту, щоб не втратити identity.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if user.Email != plan.Email.ValueString() {
		resp.Diagnostics.AddError("Некоректний email відповіді", "UUID збережено; перевірте identity перед подальшим apply.")
	}
}

// Read видаляє resource зі state тільки за authoritative 404 точкового GET.
func (r *externalUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state externalUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user, err := r.client.Read(ctx, state.OrganizationID.ValueString(), state.ID.ValueString())
	if errors.Is(err, adminapi.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Не вдалося прочитати користувача", err.Error())
		return
	}
	state.Email = types.StringValue(user.Email)
	state.State = types.StringValue(user.State)
	state.Name = types.StringNull()
	if user.Name != nil {
		state.Name = types.StringValue(*user.Name)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update змінює тільки локальну причину майбутнього видалення.
func (r *externalUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state externalUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.OrganizationID.Equal(state.OrganizationID) || !plan.Email.Equal(state.Email) || !plan.Name.Equal(state.Name) || !plan.ID.Equal(state.ID) || !plan.CreateRequestID.Equal(state.CreateRequestID) {
		resp.Diagnostics.AddError("Update identity заборонений", "Дозволена лише зміна deletion_reason у state.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete покладається на повторну серверну перевірку типу й прав перед mutation.
func (r *externalUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state externalUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, state.OrganizationID.ValueString(), state.ID.ValueString(), state.Email.ValueString(), state.DeletionReason.ValueString()); err != nil {
		resp.Diagnostics.AddError("Не вдалося видалити користувача", err.Error())
	}
}

// ImportState приймає organization_id/identity_UUID/request_UUID; Read перевіряє identity.
func (r *externalUserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 3 {
		resp.Diagnostics.AddError("Некоректний import ID", "Очікується organization_id/identity_UUID/request_UUID.")
		return
	}
	org, id, key := parts[0], parts[1], parts[2]
	_, keyErr := uuid.Parse(key)
	if _, err := uuid.Parse(id); keyErr != nil || err != nil || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`).MatchString(org) {
		resp.Diagnostics.AddError("Некоректний import ID", "Очікується organization_id/identity_UUID/request_UUID.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), org)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("create_request_id"), key)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("deletion_reason"), defaultDeletionReason)...)
}
