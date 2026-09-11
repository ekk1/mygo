package openai

// DefaultImageGenerationToolModel is the current fast, general-purpose image
// model used by ImageGeneration when no model override is supplied.
const DefaultImageGenerationToolModel = "gpt-image-2.5-flare"

// WebSearchOptions configures the native Responses web search tool.
type WebSearchOptions struct {
	Type               string
	Filters            any
	UserLocation       any
	SearchContextSize  string
	ExternalWebAccess  *bool
	ReturnTokenBudget  string
	SearchContentTypes []string
}

// WebSearchTool is a native Responses web search tool definition.
type WebSearchTool struct {
	Type               string   `json:"type"`
	Filters            any      `json:"filters,omitempty"`
	UserLocation       any      `json:"user_location,omitempty"`
	SearchContextSize  string   `json:"search_context_size,omitempty"`
	ExternalWebAccess  *bool    `json:"external_web_access,omitempty"`
	ReturnTokenBudget  string   `json:"return_token_budget,omitempty"`
	SearchContentTypes []string `json:"search_content_types,omitempty"`
}

// WebSearch constructs an enabled web search tool. Type defaults to
// "web_search" and can be overridden for legacy or compatible endpoints.
func WebSearch(options WebSearchOptions) WebSearchTool {
	toolType := options.Type
	if toolType == "" {
		toolType = "web_search"
	}
	return WebSearchTool{
		Type: toolType, Filters: options.Filters, UserLocation: options.UserLocation,
		SearchContextSize: options.SearchContextSize, ExternalWebAccess: options.ExternalWebAccess,
		ReturnTokenBudget: options.ReturnTokenBudget, SearchContentTypes: options.SearchContentTypes,
	}
}

// ImageGenerationOptions configures the native Responses image generation tool.
type ImageGenerationOptions struct {
	Type              string
	Model             string
	Action            string
	Background        string
	InputFidelity     string
	InputImageMask    any
	Moderation        string
	OutputCompression *int
	OutputFormat      string
	PartialImages     *int
	Quality           string
	Size              string
}

// ImageGenerationTool is a native Responses image generation tool definition.
type ImageGenerationTool struct {
	Type              string `json:"type"`
	Model             string `json:"model,omitempty"`
	Action            string `json:"action,omitempty"`
	Background        string `json:"background,omitempty"`
	InputFidelity     string `json:"input_fidelity,omitempty"`
	InputImageMask    any    `json:"input_image_mask,omitempty"`
	Moderation        string `json:"moderation,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	PartialImages     *int   `json:"partial_images,omitempty"`
	Quality           string `json:"quality,omitempty"`
	Size              string `json:"size,omitempty"`
}

// ImageGeneration constructs an enabled image generation tool. Model defaults
// to DefaultImageGenerationToolModel and remains freely overrideable.
func ImageGeneration(options ImageGenerationOptions) ImageGenerationTool {
	toolType := options.Type
	if toolType == "" {
		toolType = "image_generation"
	}
	model := options.Model
	if model == "" {
		model = DefaultImageGenerationToolModel
	}
	return ImageGenerationTool{
		Type: toolType, Model: model, Action: options.Action, Background: options.Background,
		InputFidelity: options.InputFidelity, InputImageMask: options.InputImageMask,
		Moderation: options.Moderation, OutputCompression: options.OutputCompression,
		OutputFormat: options.OutputFormat, PartialImages: options.PartialImages,
		Quality: options.Quality, Size: options.Size,
	}
}

// AutoContainer asks OpenAI to create or reuse a Code Interpreter container.
type AutoContainer struct {
	Type          string   `json:"type"`
	MemoryLimit   string   `json:"memory_limit,omitempty"`
	FileIDs       []string `json:"file_ids,omitempty"`
	NetworkPolicy any      `json:"network_policy,omitempty"`
}

// MarshalJSON applies the native "auto" type when it is omitted.
func (c AutoContainer) MarshalJSON() ([]byte, error) {
	type autoContainerAlias AutoContainer
	if c.Type == "" {
		c.Type = "auto"
	}
	return marshalFields(autoContainerAlias(c), nil)
}

// CodeInterpreterOptions configures the Responses Code Interpreter tool.
// Container accepts an AutoContainer, an existing container ID string, or
// another native JSON-compatible container shape.
type CodeInterpreterOptions struct {
	Type           string
	Container      any
	AllowedCallers []string
}

// CodeInterpreterTool is a native Responses Code Interpreter tool definition.
type CodeInterpreterTool struct {
	Type           string   `json:"type"`
	Container      any      `json:"container"`
	AllowedCallers []string `json:"allowed_callers,omitempty"`
}

// CodeInterpreter constructs an enabled Code Interpreter tool.
func CodeInterpreter(options CodeInterpreterOptions) CodeInterpreterTool {
	toolType := options.Type
	if toolType == "" {
		toolType = "code_interpreter"
	}
	container := options.Container
	if container == nil {
		container = AutoContainer{}
	}
	return CodeInterpreterTool{Type: toolType, Container: container, AllowedCallers: options.AllowedCallers}
}

// ShellEnvironment describes local, automatically hosted, or referenced
// container execution for the native shell tool.
type ShellEnvironment struct {
	Type          string   `json:"type"`
	ContainerID   string   `json:"container_id,omitempty"`
	FileIDs       []string `json:"file_ids,omitempty"`
	MemoryLimit   string   `json:"memory_limit,omitempty"`
	NetworkPolicy any      `json:"network_policy,omitempty"`
	Skills        []any    `json:"skills,omitempty"`
}

// HostedShellOptions configures the Responses shell tool.
type HostedShellOptions struct {
	Type           string
	Environment    ShellEnvironment
	AllowedCallers []string
}

// HostedShellTool is a native Responses shell tool definition.
type HostedShellTool struct {
	Type           string           `json:"type"`
	Environment    ShellEnvironment `json:"environment"`
	AllowedCallers []string         `json:"allowed_callers,omitempty"`
}

// HostedShell constructs an enabled native shell tool. An omitted environment
// type defaults to OpenAI-hosted automatic container provisioning.
func HostedShell(options HostedShellOptions) HostedShellTool {
	toolType := options.Type
	if toolType == "" {
		toolType = "shell"
	}
	environment := options.Environment
	if environment.Type == "" {
		environment.Type = "container_auto"
	}
	return HostedShellTool{Type: toolType, Environment: environment, AllowedCallers: options.AllowedCallers}
}

// FunctionTool defines a caller-implemented Responses function. Parameters
// should be a JSON Schema object; nil omits it.
type FunctionTool struct {
	Type           string   `json:"type"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Parameters     any      `json:"parameters,omitempty"`
	Strict         *bool    `json:"strict,omitempty"`
	AllowedCallers []string `json:"allowed_callers,omitempty"`
	Async          *bool    `json:"async,omitempty"`
	DeferLoading   *bool    `json:"defer_loading,omitempty"`
	OutputSchema   any      `json:"output_schema,omitempty"`
}

// Function constructs a native Responses function definition.
func Function(name, description string, parameters any, strict *bool) FunctionTool {
	return FunctionTool{Type: "function", Name: name, Description: description, Parameters: parameters, Strict: strict}
}
