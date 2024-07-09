package postgresflex

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/stackitcloud/terraform-provider-stackit/stackit/internal/core"
	"github.com/stackitcloud/terraform-provider-stackit/stackit/internal/validate"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackitcloud/stackit-sdk-go/core/config"
	"github.com/stackitcloud/stackit-sdk-go/core/oapierror"
	"github.com/stackitcloud/stackit-sdk-go/services/postgresflex"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &databaseResource{}
	_ resource.ResourceWithConfigure   = &databaseResource{}
	_ resource.ResourceWithImportState = &databaseResource{}
)

type Model struct {
	Id         types.String `tfsdk:"id"` // needed by TF
	DatabaseId types.String `tfsdk:"database_id"`
	InstanceId types.String `tfsdk:"instance_id"`
	ProjectId  types.String `tfsdk:"project_id"`
	Name       types.String `tfsdk:"name"`
	Owner      types.String `tfsdk:"owner"`
}

// NewDatabaseResource is a helper function to simplify the provider implementation.
func NewDatabaseResource() resource.Resource {
	return &databaseResource{}
}

// databaseResource is the resource implementation.
type databaseResource struct {
	client *postgresflex.APIClient
}

// Metadata returns the resource type name.
func (r *databaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_postgresflex_database"
}

// Configure adds the provider configured client to the resource.
func (r *databaseResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	providerData, ok := req.ProviderData.(core.ProviderData)
	if !ok {
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error configuring API client", fmt.Sprintf("Expected configure type stackit.ProviderData, got %T", req.ProviderData))
		return
	}

	var apiClient *postgresflex.APIClient
	var err error
	if providerData.PostgresFlexCustomEndpoint != "" {
		apiClient, err = postgresflex.NewAPIClient(
			config.WithCustomAuth(providerData.RoundTripper),
			config.WithEndpoint(providerData.PostgresFlexCustomEndpoint),
		)
	} else {
		apiClient, err = postgresflex.NewAPIClient(
			config.WithCustomAuth(providerData.RoundTripper),
			config.WithRegion(providerData.Region),
		)
	}

	if err != nil {
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error configuring API client", fmt.Sprintf("Configuring client: %v. This is an error related to the provider configuration, not to the resource configuration", err))
		return
	}

	r.client = apiClient
	tflog.Info(ctx, "Postgres Flex database client configured")
}

// Schema defines the schema for the resource.
func (r *databaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	descriptions := map[string]string{
		"main":        "Postgres Flex database resource schema. Must have a `region` specified in the provider configuration.",
		"id":          "Terraform's internal resource ID. It is structured as \"`project_id`,`instance_id`,`database_id`\".",
		"database_id": "Database ID.",
		"instance_id": "ID of the Postgres Flex instance.",
		"project_id":  "STACKIT project ID to which the instance is associated.",
		"name":        "Database name.",
		"owner":       "Username of the database owner.",
	}

	resp.Schema = schema.Schema{
		Description: descriptions["main"],
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: descriptions["id"],
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"database_id": schema.StringAttribute{
				Description: descriptions["database_id"],
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					validate.NoSeparator(),
				},
			},
			"instance_id": schema.StringAttribute{
				Description: descriptions["instance_id"],
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					validate.UUID(),
					validate.NoSeparator(),
				},
			},
			"project_id": schema.StringAttribute{
				Description: descriptions["project_id"],
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					validate.UUID(),
					validate.NoSeparator(),
				},
			},
			"name": schema.StringAttribute{
				Description: descriptions["name"],
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"owner": schema.StringAttribute{
				Description: descriptions["owner"],
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

// Create creates the resource and sets the initial Terraform state.
func (r *databaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) { // nolint:gocritic // function signature required by Terraform
	var model Model
	diags := req.Plan.Get(ctx, &model)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectId := model.ProjectId.ValueString()
	instanceId := model.InstanceId.ValueString()
	ctx = tflog.SetField(ctx, "project_id", projectId)
	ctx = tflog.SetField(ctx, "instance_id", instanceId)

	// Generate API request body from model
	payload, err := toCreatePayload(&model)
	if err != nil {
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error creating database", fmt.Sprintf("Creating API payload: %v", err))
		return
	}
	// Create new database
	databaseResp, err := r.client.CreateDatabase(ctx, projectId, instanceId).CreateDatabasePayload(*payload).Execute()
	if err != nil {
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error creating database", fmt.Sprintf("Calling API: %v", err))
		return
	}
	if databaseResp == nil || databaseResp.Id == nil || *databaseResp.Id == "" {
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error creating database", "API didn't return database Id. A database might have been created")
		return
	}
	databaseId := *databaseResp.Id
	ctx = tflog.SetField(ctx, "database_id", databaseId)

	database, err := getDatabase(ctx, r.client, projectId, instanceId, databaseId)
	if err != nil {
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error creating database", fmt.Sprintf("Getting database details after creation: %v", err))
		return
	}

	// Map response body to schema
	err = mapFields(database, &model)
	if err != nil {
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error creating database", fmt.Sprintf("Processing API payload: %v", err))
		return
	}
	// Set state to fully populated data
	diags = resp.State.Set(ctx, model)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Info(ctx, "Postgres Flex database created")
}

// Read refreshes the Terraform state with the latest data.
func (r *databaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) { // nolint:gocritic // function signature required by Terraform
	var model Model
	diags := req.State.Get(ctx, &model)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectId := model.ProjectId.ValueString()
	instanceId := model.InstanceId.ValueString()
	databaseId := model.DatabaseId.ValueString()
	ctx = tflog.SetField(ctx, "project_id", projectId)
	ctx = tflog.SetField(ctx, "instance_id", instanceId)
	ctx = tflog.SetField(ctx, "database_id", databaseId)

	databaseResp, err := getDatabase(ctx, r.client, projectId, instanceId, databaseId)
	if err != nil {
		oapiErr, ok := err.(*oapierror.GenericOpenAPIError) //nolint:errorlint //complaining that error.As should be used to catch wrapped errors, but this error should not be wrapped
		if ok && oapiErr.StatusCode == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error reading database", fmt.Sprintf("Calling API: %v", err))
		return
	}

	// Map response body to schema
	err = mapFields(databaseResp, &model)
	if err != nil {
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error reading database", fmt.Sprintf("Processing API payload: %v", err))
		return
	}

	// Set refreshed state
	diags = resp.State.Set(ctx, model)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Info(ctx, "Postgres Flex database read")
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *databaseResource) Update(ctx context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) { // nolint:gocritic // function signature required by Terraform
	// Update shouldn't be called
	core.LogAndAddError(ctx, &resp.Diagnostics, "Error updating database", "Database can't be updated")
}

// Delete deletes the resource and removes the Terraform state on success.
func (r *databaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) { // nolint:gocritic // function signature required by Terraform
	// Retrieve values from plan
	var model Model
	diags := req.State.Get(ctx, &model)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	projectId := model.ProjectId.ValueString()
	instanceId := model.InstanceId.ValueString()
	databaseId := model.DatabaseId.ValueString()
	ctx = tflog.SetField(ctx, "project_id", projectId)
	ctx = tflog.SetField(ctx, "instance_id", instanceId)
	ctx = tflog.SetField(ctx, "database_id", databaseId)

	// Delete existing record set
	err := r.client.DeleteDatabase(ctx, projectId, instanceId, databaseId).Execute()
	if err != nil {
		core.LogAndAddError(ctx, &resp.Diagnostics, "Error deleting database", fmt.Sprintf("Calling API: %v", err))
	}
	tflog.Info(ctx, "Postgres Flex database deleted")
}

// ImportState imports a resource into the Terraform state on success.
// The expected format of the resource import identifier is: project_id,zone_id,record_set_id
func (r *databaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	idParts := strings.Split(req.ID, core.Separator)
	if len(idParts) != 3 || idParts[0] == "" || idParts[1] == "" || idParts[2] == "" {
		core.LogAndAddError(ctx, &resp.Diagnostics,
			"Error importing database",
			fmt.Sprintf("Expected import identifier with format [project_id],[instance_id],[database_id], got %q", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), idParts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("instance_id"), idParts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("database_id"), idParts[2])...)
	core.LogAndAddWarning(ctx, &resp.Diagnostics,
		"Postgresflex database imported with empty password",
		"The database password is not imported as it is only available upon creation of a new database. The password field will be empty.",
	)
	tflog.Info(ctx, "Postgres Flex database state imported")
}

func mapFields(databaseResp *postgresflex.InstanceDatabase, model *Model) error {
	if databaseResp == nil {
		return fmt.Errorf("response is nil")
	}
	if databaseResp.Id == nil || *databaseResp.Id == "" {
		return fmt.Errorf("id not present")
	}
	if model == nil {
		return fmt.Errorf("model input is nil")
	}

	var databaseId string
	if model.DatabaseId.ValueString() != "" {
		databaseId = model.DatabaseId.ValueString()
	} else if databaseResp.Id != nil {
		databaseId = *databaseResp.Id
	} else {
		return fmt.Errorf("database id not present")
	}
	idParts := []string{
		model.ProjectId.ValueString(),
		model.InstanceId.ValueString(),
		databaseId,
	}
	model.Id = types.StringValue(
		strings.Join(idParts, core.Separator),
	)
	model.DatabaseId = types.StringValue(databaseId)
	model.Name = types.StringPointerValue(databaseResp.Name)

	if databaseResp.Options != nil {
		owner, ok := (*databaseResp.Options)["owner"]
		if ok {
			ownerStr, ok := owner.(string)
			if !ok {
				return fmt.Errorf("owner is not a string")
			}
			model.Owner = types.StringValue(ownerStr)
		}
	}

	return nil
}

func toCreatePayload(model *Model) (*postgresflex.CreateDatabasePayload, error) {
	if model == nil {
		return nil, fmt.Errorf("nil model")
	}

	return &postgresflex.CreateDatabasePayload{
		Name: model.Name.ValueStringPointer(),
		Options: &map[string]string{
			"owner": model.Owner.ValueString(),
		},
	}, nil
}

// The API does not have a GetDatabase endpoint, only ListDatabases
func getDatabase(ctx context.Context, client *postgresflex.APIClient, projectId, instanceId, databaseId string) (*postgresflex.InstanceDatabase, error) {
	resp, err := client.ListDatabases(ctx, projectId, instanceId).Execute()
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Databases == nil {
		return nil, fmt.Errorf("response is nil")
	}
	for _, database := range *resp.Databases {
		if database.Id != nil && *database.Id == databaseId {
			return &database, nil
		}
	}
	return nil, fmt.Errorf("database not found")
}
