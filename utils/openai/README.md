# openai

直接调用 OpenAI 原生生成、图片、语音、Files、Containers 和 Batch API，支持 HTTP 代理和逐请求调试日志。

## 客户端与通用约定

```go
type Config struct {
    APIKey, BaseURL, ProxyURL string
    Timeout time.Duration
    Headers http.Header
    Debug bool
    DebugDir string
    DebugOmitResponseBody bool
    MaxResponseBytes int64
    CAFile, CertFile, KeyFile string
}
func New(cfg Config) (*Client, error)
func (c *Client) CloseIdleConnections()
func Ptr[T any](v T) *T

type HTTPResponse = httpclient.Response // StatusCode int; Header http.Header; Body []byte
type Event struct { Type, ID string; Data json.RawMessage } // 实际为内部 SSE 类型的别名
type Upload struct { Field, Filename, ContentType string; Reader io.Reader }
func (c *Client) DownloadMedia(ctx context.Context, rawURL string, dst io.Writer) (*HTTPResponse, error)
```

- `New` 创建独立连接池，默认 `BaseURL=https://api.openai.com/v1`。可改为第三方代理的完整 API 前缀；Headers 可配置组织、项目等请求头，也可用显式 Authorization 替代 APIKey。创建时复制 headers，不读取环境中的 API key。
- `ProxyURL` 与 [httpclient](../httpclient/README.md) 相同：空值使用标准环境代理；`"-"` 直连；支持 http/https/socks5/socks5h 及代理认证。所有 API、上传、下载、SSE 均使用同一代理与证书配置。
- `Timeout` 默认 10 分钟，包含读取响应；`context` 可提前取消。`MaxResponseBytes` 默认 256 MiB，限制缓冲响应和单个 SSE 事件；二进制下载流向 writer，不受此缓冲限制。负超时/限制、非法 URL/代理/证书或缺少认证返回 error。
- 客户端创建后只读，可并发复用；请求参数、Reader、Writer 及回调由调用方管理，不可在使用期间并发修改。Upload.Reader 不由客户端关闭；自定义阻塞 Reader 必须由调用方提供可终止的读取行为。
- 所有 `Extra map[string]any` 用于附加原生字段，与已编码字段重名时报错。指针区分未设置和显式 false/0；使用 `Ptr(false)` 等赋值。模型、服务档位、工具版本保持原生字符串，无本地模型白名单。
- 工具只在加入请求 `Tools` 时开启；不加入即关闭。普通请求不注入额外工具、service tier、beta header。不会自动更换模型、重试、跟随重定向、轮询、执行本地工具或清理云端资源。
- 解码结果的 `HTTP` 保留原始 JSON、状态、headers（可读 `X-Request-ID`）。非 2xx 返回 `*httpclient.StatusError`，可用 `errors.As` 提取；网络/context 错误保留错误链。读取、解码、回调、writer 和日志写入错误均返回，不忽略部分结果。
- SSE 是单次 HTTP 响应，保留原生事件；不会合并成统一文本。提前 EOF 返回 `io.ErrUnexpectedEOF`。端点自身的失败状态、工具输出仍需按原生协议处理。
- `DownloadMedia` 下载显式媒体 URL，复用代理/debug，但不携带 API key 或自定义默认 headers。认证 Files/Containers 下载使用各自方法；下载 URL 过期时返回原始错误，不自动重新生成。
- 暂不接入 OpenAI Videos、Live/Realtime、MCP、Vector Stores。无需第三方 Go SDK 或本地媒体编解码工具。

## Debug

`Debug=true` 时，`DebugDir` 默认 `./ai-debug`。每次 HTTP 调用创建独立目录（0700），保存以下文件（0600）：

| 文件 | 内容 |
| --- | --- |
| request.json | 方法、脱敏 URL/headers、开始时间 |
| request.body | 实际读取并发送的正文，包含 multipart/文件内容 |
| response.body | 实际读取的原始正文、二进制或 SSE，随接收落盘；`DebugOmitResponseBody=true` 时不创建 |
| response.json | 状态、脱敏 headers、耗时、字节数、完整性及失败标记 |

认证头、API key 和签名查询参数脱敏；正文按原样保留，可能包含提示词、上传文件和生成内容。中断只保存已传输部分并标明不完整；没有响应也保留记录。日志创建/写入/关闭失败返回 error，不能把这种错误当作请求未执行而直接重试。并发请求各自记录；关闭 Debug 不创建日志。`response_complete` 仅表示 HTTP 正文已读到 EOF，不表示生成成功；缺少 SSE 终止事件等协议校验错误以方法返回的 error 为准。

`DebugOmitResponseBody=true` 只保留请求正文与请求/响应元数据，响应仍正常返回给调用方，`response.json` 仍记录响应字节数和完整性。

## Models

```go
func (c *Client) ListModels(ctx context.Context) (*ModelList, error)
type Model struct {
    ID, Object string
    Created int64
    OwnedBy string
    Raw json.RawMessage
}
func (*Model) UnmarshalJSON([]byte) error
func (Model) MarshalJSON() ([]byte, error)
type ModelList struct {
    Object string
    Data []Model
    HTTP *HTTPResponse
}
```

`ListModels` 返回 provider 的 `/models` 原生目录；`HTTP` 保留状态、响应头和原始正文。`Model.UnmarshalJSON` 解码常用字段并在 `Raw` 中保留完整原生 metadata；`MarshalJSON` 优先原样输出 `Raw`。需要修改已解码字段时，先将该模型的 `Raw` 置 nil。

## 最小示例

以下示例中的 `model`、`imageModel` 和 `apiKey` 由调用方提供；音频示例中的模型与音色也需按实际能力选择。

```go
c, err := openai.New(openai.Config{
    APIKey: apiKey,
    ProxyURL: "http://127.0.0.1:7890", // 无代理用 "-"
    Debug: true,
    DebugDir: "./ai-debug",
})
if err != nil { return err }
defer c.CloseIdleConnections()

res, err := c.CreateResponse(ctx, openai.ResponseRequest{
    Model: model,
    Input: "分析一下今天值得关注的科技新闻",
    Tools: []any{openai.WebSearch(openai.WebSearchOptions{})},
    ServiceTier: "flex", // 不需要则省略；由服务端决定模型是否支持
})
if err != nil { return err }
for _, item := range res.Output {
    for _, part := range item.Content {
        if part.Type == "output_text" { fmt.Println(part.Text) }
    }
}
```

以上是函数内片段；包路径为 `github.com/ekk1/mygo/utils/openai`。`ctx` 为调用方的 context。

## 核心生成：Chat Completions 与 Responses

`ChatCompletionRequest` 和 `ResponseRequest` 使用 OpenAI 原生 JSON 字段；`Model` 不限制枚举，多态消息内容、结构化输出、reasoning、工具和未来原生对象使用 `any` 或 `json.RawMessage`。可选布尔和数值使用指针区分“省略”和显式 `false`/`0`；可选字符串为空时省略。具体请求字段见下表，`Extra` 和错误行为遵循开头的通用约定。

### 方法

```go
func (c *Client) CreateChatCompletion(ctx context.Context, request ChatCompletionRequest) (*ChatCompletion, error)
func (c *Client) StreamChatCompletion(ctx context.Context, request ChatCompletionRequest, handle func(Event) error) (*HTTPResponse, error)

func (c *Client) CreateResponse(ctx context.Context, request ResponseRequest) (*Response, error)
func (c *Client) StreamResponse(ctx context.Context, request ResponseRequest, handle func(Event) error) (*HTTPResponse, error)
func (c *Client) GetResponse(ctx context.Context, responseID string, optional ...ResponseGetOptions) (*Response, error)
func (c *Client) CancelResponse(ctx context.Context, responseID string) (*Response, error)
func (c *Client) DeleteResponse(ctx context.Context, responseID string) (*HTTPResponse, error)
func (c *Client) ListResponseInputItems(ctx context.Context, responseID string, options ResponseInputItemsOptions) (*ResponseItemList, error)
func (c *Client) CountResponseInputTokens(ctx context.Context, request ResponseInputTokensRequest) (*ResponseInputTokens, error)
func (c *Client) CompactResponse(ctx context.Context, request ResponseCompactRequest) (*CompactedResponse, error)
```

`CreateChatCompletion` 和 `CreateResponse` 保留未设置的 `stream` 字段为省略状态；传入 `stream:true` 会在请求发出前报错并提示调用对应流式方法。`GetResponse` 可省略 options，或传一个 `ResponseGetOptions` 设置 `Include []string`、`IncludeObfuscation *bool`、`StartingAfter *int64`；传多个 options 会报错。`ListResponseInputItems` 支持 `After`、`Include`、`Limit` 和 `Order`。资源 ID 必须为非空不透明 ID，不能包含斜杠、点路径或查询片段。

流式回调接收未经改写的 `Event{Type, ID, Data}`。Chat 必须收到 `[DONE]`；Responses 必须收到 `response.completed`、`response.incomplete` 或 `response.failed`。正常 EOF 未出现终止事件时返回 `io.ErrUnexpectedEOF`。`response.incomplete` 是可检查的正常终止；`response.failed`、`error`、Chat 错误 JSON 和畸形 JSON 会在回调先看到原始事件后返回 `*StreamError`，其 `Event` 保存原始数据，`errors.As` 可提取它，`errors.Is`/`errors.As` 可继续检查其底层错误。回调自身的错误原样传播。nil 回调会在发请求前报错。

### 请求类型

| 类型 | 字段与用途 |
| --- | --- |
| `ChatCompletionRequest` | 必需 `Model string`、`Messages []ChatMessage`；可选布尔/数值指针 `FrequencyPenalty`、`Logprobs`、`MaxCompletionTokens`、`MaxTokens`、`N`、`ParallelToolCalls`、`PresencePenalty`、`Seed`、`Store`、`Stream`、`Temperature`、`TopLogprobs`、`TopP`；字符串 `PromptCacheKey`、`PromptCacheRetention`、`ReasoningEffort`、`SafetyIdentifier`、`ServiceTier`、`User`、`Verbosity`；原生对象 `Audio`、`FunctionCall`、`Moderation`、`Prediction`、`PromptCacheOptions`、`ResponseFormat`、`Stop`、`StreamOptions`、`ToolChoice`、`WebSearchOptions`；集合 `Functions []any`、`LogitBias map[string]int`、`Metadata map[string]string`、`Modalities []string`、`Tools []any`；`Extra` 补充未来字段。`ResponseFormat` 可直接传原生 JSON Schema structured output。 |
| `ResponseRequest` | 可选布尔/数值指针 `Background`、`MaxOutputTokens`、`MaxToolCalls`、`ParallelToolCalls`、`Store`、`Stream`、`Temperature`、`TopLogprobs`、`TopP`；字符串 `Model`、`PreviousResponseID`、`PromptCacheKey`、`PromptCacheRetention`、`SafetyIdentifier`、`ServiceTier`、`Truncation`、`User`；原生对象 `ContextManagement`、`Conversation`、`Input`、`Instructions`、`Moderation`、`Prompt`、`PromptCacheOptions`、`Reasoning`、`StreamOptions`、`Text`、`ToolChoice`；集合 `Include []string`、`Metadata map[string]string`、`Tools []any`；`Extra` 补充未来字段。结构化输出放在 `Text` 的原生 `format` 对象中。 |
| `ResponseInputTokensRequest` | `Conversation`、`Input`、`Instructions`、`Prompt`、`Reasoning`、`Text`、`ToolChoice` 为原生对象；`Model`、`Personality`、`PreviousResponseID`、`Truncation` 为字符串；`ParallelToolCalls *bool`、`Tools []any`；`Extra` 补充未来字段。 |
| `ResponseCompactRequest` | `Input`、`Instructions`、`PromptCacheOptions` 为原生对象；`Model`、`PreviousResponseID`、`PromptCacheKey`、`PromptCacheRetention`、`ServiceTier` 为字符串；`Extra` 补充未来字段。 |

`ChatMessage` 字段为 `Role string`、`Content any`、可选 `Name string`、`Audio any`、`Annotations json.RawMessage`、`FunctionCall any`、`Refusal string`、`ToolCallID string` 和 `ToolCalls []ChatToolCall`。`Content` 可传字符串、`nil`（显式 JSON null）或原生多模态 content part 数组。`ChatToolCall` 包含 `ID`、`Type`、`Function *ChatFunctionCall`、`Custom json.RawMessage`；`ChatFunctionCall` 包含 `Name` 与 JSON 字符串 `Arguments`。

`ChatMessage` 同时用于输入和输出，客户端不自动管理会话或转换历史。续聊时应按输入 schema 构造助手消息：去除输出专用 `Annotations`，`Audio` 仅保留 `id`。工作台的历史管理属于 [会话层](../../cmd/workbench/DEVELOPMENT.md#上下文回传)。

Responses 常用输入可用 `ResponseInputMessage{Type, Role, Content, Status}`；`Content` 可为字符串或 `[]ResponseInputContent`。`ResponseInputContent` 提供 `Type`、`Text`、`ImageURL`、`Detail`、`FileID`、`FileURL`、`FileData`、`Filename` 和原生 `PromptCacheBreakpoint`，覆盖常用 `input_text`、`input_image`、`input_file`。其他 item 直接传原生 JSON。

所有上述请求类型实现 `MarshalJSON() ([]byte, error)`，将 `Extra` 与类型字段合并；`AutoContainer` 也实现 `MarshalJSON() ([]byte, error)`，为空的 `Type` 写为 `auto`。

### 结果类型

| 类型 | 已解码字段 |
| --- | --- |
| `ChatCompletion` | `ID`、`Object`、`Created`、`Model`、`Choices []ChatCompletionChoice`、`Usage`、`ServiceTier`、`SystemFingerprint`、`Metadata`、原生 `Moderation`、`HTTP`。 |
| `ChatCompletionChoice` | `Index`、`Message ChatMessage`、`FinishReason`、原生 `Logprobs`。 |
| `Response` | `ID`、`Object`、`CreatedAt`、`CompletedAt`、`Status`、原生 `Error`/`IncompleteDetails`/`Instructions`/`Reasoning`/`Text`/`ToolChoice`/`Tools`，以及 token 限制、metadata、model、`Output []ResponseOutputItem`、并行工具、上一响应 ID、service tier、`Usage`、`HTTP`。 |
| `ResponseOutputItem` | 常用 `ID`、`Type`、`Status`、`Role`、`Content`、`Name`、`CallID`、`Arguments`、`Output`、`ContainerID`、`EncryptedContent`，以及完整 `Raw`。实现 `UnmarshalJSON([]byte) error` 和 `MarshalJSON() ([]byte, error)`。 |
| `ResponseContent` | `Type`、`Text`、`Refusal`、原生 `Annotations`、`ImageURL`、`FileID`、`Filename`，以及完整 `Raw`。实现 `UnmarshalJSON([]byte) error` 和 `MarshalJSON() ([]byte, error)`。 |
| `Usage` | Chat 的 `PromptTokens`/`CompletionTokens`、Responses 的 `InputTokens`/`OutputTokens`、共同的 `TotalTokens`，以及四种 endpoint-specific details 原始 JSON。 |
| `ResponseItemList` | `Object`、`Data []ResponseOutputItem`、`FirstID`、`LastID`、`HasMore`、`HTTP`。 |
| `ResponseInputTokens` | `Object`、`InputTokens`、`HTTP`。 |
| `CompactedResponse` | `ID`、`Object`、`CreatedAt`、`Output []ResponseOutputItem`、`Usage`、`HTTP`。 |

`ResponseGetOptions` 有 `Include []string`、`IncludeObfuscation *bool`、`StartingAfter *int64`。`ResponseInputItemsOptions` 有 `After string`、`Include []string`、`Limit int`、`Order string`。`StreamError` 有 `Event Event` 与 `Err error`，并实现 `Error() string`、`Unwrap() error`。`ResponseOutputItem.Raw` 和 `ResponseContent.Raw` 分别保存完整原生 item/content JSON；其 MarshalJSON 默认原样写回 `Raw`，因此 compaction output 可无损作为后续 input 使用。若要修改已解码字段，先将对应值的 `Raw` 置 nil；修改嵌套 content 时，外层 item.Raw 和该 content.Raw 都须清空。顶层未知字段通过 `HTTP.Body` 保留。

### 原生工具构造函数

```go
const DefaultImageGenerationToolModel = "gpt-image-2.5-flare"

func WebSearch(options WebSearchOptions) WebSearchTool
func ImageGeneration(options ImageGenerationOptions) ImageGenerationTool
func CodeInterpreter(options CodeInterpreterOptions) CodeInterpreterTool
func HostedShell(options HostedShellOptions) HostedShellTool
func Function(name, description string, parameters any, strict *bool) FunctionTool
```

| Options / 返回类型 | 字段、默认值与编码 |
| --- | --- |
| `WebSearchOptions` / `WebSearchTool` | `Type` 默认 `web_search`，可覆盖为旧版或兼容代理值；`Filters any`、`UserLocation any`、`SearchContextSize string`、`ExternalWebAccess *bool`、`ReturnTokenBudget string`、`SearchContentTypes []string` 原样映射。 |
| `ImageGenerationOptions` / `ImageGenerationTool` | `Type` 默认 `image_generation`，`Model` 默认 `gpt-image-2.5-flare`，两者可覆盖；另有 `Action`、`Background`、`InputFidelity`、`InputImageMask any`、`Moderation`、`OutputCompression *int`、`OutputFormat`、`PartialImages *int`、`Quality`、`Size`。 |
| `CodeInterpreterOptions` / `CodeInterpreterTool` | `Type` 默认 `code_interpreter`；`Container any` 接受 `AutoContainer`、已有容器 ID 字符串或原生 JSON；nil 默认 `AutoContainer{}`；`AllowedCallers []string`。`AutoContainer` 有 `Type`（默认 `auto`）、`MemoryLimit`、`FileIDs`、`NetworkPolicy any`。 |
| `HostedShellOptions` / `HostedShellTool` | `Type` 默认 `shell`；`Environment ShellEnvironment`；`AllowedCallers []string`。空环境默认 `container_auto`。`ShellEnvironment` 有 `Type`、`ContainerID`、`FileIDs`、`MemoryLimit`、`NetworkPolicy any`、`Skills []any`，支持 hosted `container_auto`、`container_reference`，也能表达原生 `local`。 |
| `FunctionTool` | `Type` 固定为 `function`；`Name`、`Description`、`Parameters any`（输入 JSON Schema）、`Strict *bool`、`AllowedCallers []string`、`Async *bool`、`DeferLoading *bool`、`OutputSchema any`。构造函数设置前五个基础字段，其他原生选项可在返回值上直接设置。 |

工具构造函数只生成原生请求 JSON，不执行本地工具循环。

### 官方参考

原生字段见 [Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create) 与 [Responses](https://developers.openai.com/api/reference/resources/responses/methods/create)。工具说明见 [Web Search](https://developers.openai.com/api/docs/guides/tools-web-search)、[图片生成](https://developers.openai.com/api/docs/guides/image-generation)、[Code Interpreter](https://developers.openai.com/api/docs/guides/tools-code-interpreter) 和 [Shell](https://developers.openai.com/api/docs/guides/tools-shell)。

## 图片与非实时音频

媒体方法复用同一个 `Client` 的代理、超时、响应大小限制和调试日志。模型与音色使用开放字符串或原生 JSON，可直接传快照名、代理别名和未来值。实现依据当前 [Images API](https://developers.openai.com/api/reference/resources/images)、[Audio API](https://developers.openai.com/api/reference/resources/audio)、[图片指南](https://developers.openai.com/api/docs/guides/image-generation)和[音频指南](https://developers.openai.com/api/docs/guides/audio)。

支持图片生成与编辑及其单向 SSE、语音合成、已完成音频文件的转写与翻译，以及转写 SSE；不创建 Live/Realtime 会话，也不提供视频接口。

### 导出接口

```go
type ImageGenerateRequest struct {
    Model, Prompt, Background, Moderation string
    N, OutputCompression, PartialImages *int
    OutputFormat, Quality, ResponseFormat, Size string
    Stream *bool
    Style, User string
    Extra map[string]any
}
func (ImageGenerateRequest) MarshalJSON() ([]byte, error)

type ImageReference struct { FileID, ImageURL string }

type ImageEditRequest struct {
    Model, Prompt string
    Images []ImageReference
    Mask *ImageReference
    Background, InputFidelity, Moderation string
    N, OutputCompression, PartialImages *int
    OutputFormat, Quality, ResponseFormat, Size string
    Stream *bool
    User string
    ImageFiles []Upload
    MaskFile *Upload
    Extra map[string]any
}
func (ImageEditRequest) MarshalJSON() ([]byte, error)

type Image struct { B64JSON, RevisedPrompt, URL string }
type ImageTokenDetails struct { ImageTokens, TextTokens int }
type ImageUsage struct {
    InputTokens int
    InputTokensDetails ImageTokenDetails
    OutputTokens int
    OutputTokensDetails ImageTokenDetails
    TotalTokens int
}
type ImageResponse struct {
    Created int64
    Background string
    Data []Image
    OutputFormat, Quality, Size string
    Usage ImageUsage
    HTTP *HTTPResponse
}
type ImageStreamEvent struct {
    Type, B64JSON, Background string
    CreatedAt int64
    OutputFormat string
    PartialImageIndex int
    Quality, Size string
    Usage ImageUsage
    Raw json.RawMessage
}
func (*ImageStreamEvent) UnmarshalJSON([]byte) error

func (c *Client) GenerateImage(context.Context, ImageGenerateRequest) (*ImageResponse, error)
func (c *Client) EditImage(context.Context, ImageEditRequest) (*ImageResponse, error)
func (c *Client) StreamImage(context.Context, ImageGenerateRequest, func(ImageStreamEvent) error) (*HTTPResponse, error)
func (c *Client) StreamImageEdit(context.Context, ImageEditRequest, func(ImageStreamEvent) error) (*HTTPResponse, error)

type SpeechRequest struct {
    Model, Input string
    Voice any
    Instructions, ResponseFormat string
    Speed *float64
    StreamFormat string
    Extra map[string]any
}
func (SpeechRequest) MarshalJSON() ([]byte, error)
type SpeechUsage struct { InputTokens, OutputTokens, TotalTokens int }
type SpeechStreamEvent struct {
    Type, Audio string
    Usage SpeechUsage
    Raw json.RawMessage
}
func (*SpeechStreamEvent) UnmarshalJSON([]byte) error
func (c *Client) CreateSpeech(context.Context, SpeechRequest, io.Writer) (*HTTPResponse, error)
func (c *Client) StreamSpeech(context.Context, SpeechRequest, func(SpeechStreamEvent) error) (*HTTPResponse, error)

type TranscriptionRequest struct {
    Model string
    File Upload
    ChunkingStrategy json.RawMessage
    Include, Keywords, KnownSpeakerNames, KnownSpeakerReferences []string
    Language string
    Languages []string
    Prompt, ResponseFormat string
    Stream *bool
    Temperature *float64
    TimestampGranularities []string
    Extra map[string]any
}
type TranslationRequest struct {
    Model string
    File Upload
    Prompt, ResponseFormat string
    Temperature *float64
    Extra map[string]any
}
type TranscriptionLanguage struct { Code string }
type TranscriptionLogprob struct { Token string; Bytes []int; Logprob float64 }
type TranscriptionUsage struct {
    Type string
    InputTokens, OutputTokens, TotalTokens int
    InputTokenDetails struct { AudioTokens, TextTokens int }
    Seconds float64
}
type TranscriptionWord struct { Word string; Start, End float64 }
type TranscriptionSegment struct {
    ID json.RawMessage
    Type string
    Seek int
    Start, End float64
    Text, Speaker string
    Tokens []int
    Temperature, AvgLogprob, CompressionRatio, NoSpeechProb float64
}
type Transcription struct {
    Task, Language string
    Languages []TranscriptionLanguage
    Duration float64
    Text string
    Words []TranscriptionWord
    Segments []TranscriptionSegment
    Logprobs []TranscriptionLogprob
    Usage TranscriptionUsage
    HTTP *HTTPResponse
}
type TranscriptionStreamEvent struct {
    Type, Delta, Text, SegmentID, ID string
    Start, End float64
    Speaker string
    Languages []TranscriptionLanguage
    Logprobs []TranscriptionLogprob
    Usage TranscriptionUsage
    Raw json.RawMessage
}
func (*TranscriptionStreamEvent) UnmarshalJSON([]byte) error

func (c *Client) CreateTranscription(context.Context, TranscriptionRequest) (*Transcription, error)
func (c *Client) CreateTranslation(context.Context, TranslationRequest) (*Transcription, error)
func (c *Client) StreamTranscription(context.Context, TranscriptionRequest, func(TranscriptionStreamEvent) error) (*HTTPResponse, error)
```

媒体方法的 `HTTP` 保留状态码和响应头；JSON/文本解码结果还保留原始正文，成功的二进制下载直接写入 writer，SSE 正文通过回调读取，不在 `HTTP.Body` 中累计。失败时先检查返回值是否非 nil，再读取 `HTTP`。

`EditImage` 在 `ImageFiles` 和 `MaskFile` 为空时发送 JSON，此时 `Images` 和 `Mask` 可使用 Files API ID、HTTPS URL 或 base64 data URL。传入文件后改用 multipart：每个 `ImageFiles` 元素作为 `image[]`，`MaskFile` 作为 `mask`。同一请求不能混用 JSON 引用和 multipart 文件。上传必须提供文件名和 reader；`ContentType` 默认为 `application/octet-stream`。

`StreamImage`、`StreamImageEdit` 和 `StreamTranscription` 分别要求收到 `image_generation.completed`、`image_edit.completed` 和 `transcript.text.done`。在终止事件前正常 EOF 会返回 `io.ErrUnexpectedEOF`。原生 `error` 事件返回保留原始 `Event` 的 `*StreamError`；类型化事件还在 `Raw` 中保留完整 JSON。回调错误会终止流并原样返回。nil 回调在发出请求前即被拒绝。

非流式方法拒绝 `stream=true`，应改用对应流式方法。`CreateSpeech` 同样拒绝 `stream_format=sse`，应使用 `StreamSpeech`，避免把 SSE 当作 JSON 或二进制音频处理。

`CreateTranscription` 和 `CreateTranslation` 会解码 `json`、`verbose_json` 和 `diarized_json`。对于 `text`、`srt` 和 `vtt`，返回文本同时存入 `Transcription.Text` 与 `Transcription.HTTP.Body`。`TimestampGranularities` 用于 `verbose_json`；分角色结果的说话人标签位于 `Segments[].Speaker`。客户端按请求转发参数组合，不猜测模型兼容性。

### 生成与编辑图片

```go
result, err := client.GenerateImage(ctx, openai.ImageGenerateRequest{
    Model:        imageModel,
    Prompt:       "a linocut fox reading beside a window",
    OutputFormat: "png",
    Size:         "1024x1024",
    Quality:      "high",
})
if err != nil { return err }
png, err := base64.StdEncoding.DecodeString(result.Data[0].B64JSON)
if err != nil { return err }
if err := os.WriteFile("fox.png", png, 0600); err != nil { return err }
```

使用原生 Files API 引用编辑：

```go
edited, err := client.EditImage(ctx, openai.ImageEditRequest{
    Model:  imageModel,
    Prompt: "add a small red hat",
    Images: []openai.ImageReference{{FileID: "file_abc"}},
    Mask:   &openai.ImageReference{ImageURL: "data:image/png;base64,..."},
})
```

使用 multipart 上传编辑：

```go
source, err := os.Open("source.png")
if err != nil { return err }
defer source.Close()
edited, err := client.EditImage(ctx, openai.ImageEditRequest{
    Model:  imageModel,
    Prompt: "place the object on a wooden desk",
    ImageFiles: []openai.Upload{{
        Filename: "source.png", ContentType: "image/png", Reader: source,
    }},
})
```

部分图片流是单个 HTTP 响应：

```go
_, err := client.StreamImage(ctx, openai.ImageGenerateRequest{
    Model: imageModel, Prompt: "a rainy neon street",
    PartialImages: openai.Ptr(2),
}, func(event openai.ImageStreamEvent) error {
    if event.Type == "image_generation.partial_image" {
        // Render or save event.B64JSON.
    }
    return nil
})
```

### 语音与已完成文件转写

```go
out, err := os.Create("speech.mp3")
if err != nil { return err }
defer out.Close()
_, err = client.CreateSpeech(ctx, openai.SpeechRequest{
    Model: "gpt-4o-mini-tts", Input: "Welcome.", Voice: "alloy",
    Instructions: "Speak warmly and clearly.", ResponseFormat: "mp3",
}, out)
```

`Voice` 也可传原生自定义音色 JSON，例如 `map[string]any{"id": "voice_1234"}`。服务端当前支持 MP3、Opus、AAC、FLAC、WAV 和 PCM。OpenAI 原始 PCM 为 24 kHz、16 位有符号小端格式，不含文件头。

语音 SSE 仍是一次非实时 HTTP 请求。增量事件包含 base64 音频，终止事件包含用量：

```go
_, err := client.StreamSpeech(ctx, openai.SpeechRequest{
    Model: "gpt-4o-mini-tts", Input: "Welcome.", Voice: "alloy",
    ResponseFormat: "pcm",
}, func(event openai.SpeechStreamEvent) error {
    if event.Type != "speech.audio.delta" { return nil }
    chunk, err := base64.StdEncoding.DecodeString(event.Audio)
    if err != nil { return err }
    _, err = output.Write(chunk)
    return err
})
```

`StreamSpeech` 会设置 `stream_format=sse`，并要求收到官方 `speech.audio.done` 终止事件。

```go
audio, err := os.Open("meeting.wav")
if err != nil { return err }
defer audio.Close()
transcript, err := client.CreateTranscription(ctx, openai.TranscriptionRequest{
    Model: "gpt-4o-transcribe-diarize",
    File: openai.Upload{Filename: "meeting.wav", ContentType: "audio/wav", Reader: audio},
    ResponseFormat: "diarized_json",
    ChunkingStrategy: json.RawMessage(`"auto"`),
    KnownSpeakerNames: []string{"agent"},
    KnownSpeakerReferences: []string{"data:audio/wav;base64,..."},
})
if err != nil { return err }
for _, segment := range transcript.Segments {
    fmt.Printf("%.2f-%.2f %s: %s\n", segment.Start, segment.End, segment.Speaker, segment.Text)
}
```

需要词级和分段时间戳时，设置 `ResponseFormat: "verbose_json"` 和 `TimestampGranularities: []string{"word", "segment"}`。`Language`、`Languages`、`Prompt`、`Keywords` 和 `Include` 按官方 multipart 字段发送。`ChunkingStrategy: json.RawMessage("\"auto\"")` 会发送标量 `chunking_strategy=auto`；手动 VAD 对象使用 `chunking_strategy[type]`、`chunking_strategy[threshold]` 等原生 bracket 字段。

翻译结果固定为英语：

```go
translated, err := client.CreateTranslation(ctx, openai.TranslationRequest{
    Model: "whisper-1",
    File: openai.Upload{Filename: "german.m4a", ContentType: "audio/mp4", Reader: audio},
    ResponseFormat: "text",
})
```

对已有录音进行流式转写，无需建立实时会话：

```go
_, err := client.StreamTranscription(ctx, openai.TranscriptionRequest{
    Model: "gpt-4o-transcribe",
    File: openai.Upload{Filename: "recording.wav", ContentType: "audio/wav", Reader: audio},
}, func(event openai.TranscriptionStreamEvent) error {
    if event.Type == "transcript.text.delta" { fmt.Print(event.Delta) }
    return nil
})
```

## Files、Containers 与 Batch

这些方法分别映射 [Files](https://developers.openai.com/api/reference/resources/files)、[Containers](https://developers.openai.com/api/reference/resources/containers) 和 [Batch](https://developers.openai.com/api/reference/resources/batches) 原生资源 API，每次只处理一次请求。分页、轮询和资源清理由调用方控制；返回值与错误遵循通用约定。

### Files

```go
func (c *Client) UploadFile(context.Context, UploadFileParams) (*File, error)
func (c *Client) ListFiles(context.Context, ListFilesParams) (*FileList, error)
func (c *Client) GetFile(context.Context, string) (*File, error)
func (c *Client) DeleteFile(context.Context, string) (*FileDeletion, error)
func (c *Client) DownloadFile(context.Context, string, io.Writer) (*HTTPResponse, error)
```

| 类型 | 字段 |
| --- | --- |
| `FileExpiration` | `Anchor string`、`Seconds int64` |
| `UploadFileParams` | `File Upload`、`Purpose string`、`ExpiresAfter *FileExpiration` |
| `ListFilesParams` | `After *string`、`Limit *int`、`Order *string`、`Purpose *string` |
| `File` | `ID`、`Object`、`Filename`、`Purpose`、`Status`、`StatusDetails string`；`Bytes`、`CreatedAt int64`；`ExpiresAt *int64`；`HTTP *HTTPResponse` |
| `FileList` | `Object`、`FirstID`、`LastID string`；`Data []File`；`HasMore bool`；`HTTP *HTTPResponse` |
| `FileDeletion` | `ID`、`Object string`；`Deleted bool`；`HTTP *HTTPResponse` |

`UploadFile` 用 multipart 流式读取 `Upload.Reader`，到期设置对应原生 `expires_after[anchor]` 与 `expires_after[seconds]` 表单字段；Reader 仍由调用方关闭。`DownloadFile` 将内容流式写入 `io.Writer`，也用于 Batch 输出与错误 JSONL；目标为 nil 时返回错误。

```go
input, err := os.Open("requests.jsonl")
if err != nil { return err }
defer input.Close()
uploaded, err := client.UploadFile(ctx, openai.UploadFileParams{
    File: openai.Upload{Filename: "requests.jsonl", ContentType: "application/jsonl", Reader: input},
    Purpose: "batch",
    ExpiresAfter: &openai.FileExpiration{Anchor: "created_at", Seconds: 86400},
})
```

列表参数使用指针区分省略与显式空值/零值。方法只返回一页；`HasMore` 为 true 时可将 `LastID` 作为下一次 `After`。

### Containers

```go
func (c *Client) CreateContainer(context.Context, CreateContainerParams) (*Container, error)
func (c *Client) ListContainers(context.Context, ListContainersParams) (*ContainerList, error)
func (c *Client) GetContainer(context.Context, string) (*Container, error)
func (c *Client) DeleteContainer(context.Context, string) (*ContainerDeletion, error)
```

| 类型 | 字段 |
| --- | --- |
| `ContainerExpiration` | `Anchor string`、`Minutes int` |
| `CreateContainerParams` | `Name string`、`ExpiresAfter *ContainerExpiration`、`FileIDs []string`、`MemoryLimit *string`、`NetworkPolicy any`、`Extra map[string]any`；实现 `MarshalJSON` |
| `ListContainersParams` | `After *string`、`Limit *int`、`Name *string`、`Order *string` |
| `Container` | `ID`、`Object`、`Status`、`MemoryLimit`、`Name string`；`CreatedAt int64`；`ExpiresAfter *ContainerExpiration`、`LastActiveAt *int64`；`NetworkPolicy json.RawMessage`；`HTTP *HTTPResponse` |
| `ContainerList` | `Object`、`FirstID`、`LastID string`；`Data []Container`；`HasMore bool`；`HTTP *HTTPResponse` |
| `ContainerDeletion` | `ID`、`Object string`；`Deleted bool`；`HTTP *HTTPResponse` |

`MemoryLimit` 保持原生字符串。`NetworkPolicy` 保留 disabled/allowlist 的多态 JSON。`Extra` 可发送未来原生字段；若覆盖一个已出现的类型化字段，序列化直接报错。

```go
container, err := client.CreateContainer(ctx, openai.CreateContainerParams{
    Name: "spreadsheet-work",
    FileIDs: []string{uploaded.ID},
    MemoryLimit: openai.Ptr("4g"),
    ExpiresAfter: &openai.ContainerExpiration{Anchor: "last_active_at", Minutes: 30},
    NetworkPolicy: map[string]any{"type": "disabled"},
})
```

容器文件接口：

```go
func (c *Client) AddContainerFile(context.Context, string, AddContainerFileParams) (*ContainerFile, error)
func (c *Client) UploadContainerFile(context.Context, string, Upload) (*ContainerFile, error)
func (c *Client) ListContainerFiles(context.Context, string, ListContainerFilesParams) (*ContainerFileList, error)
func (c *Client) GetContainerFile(context.Context, string, string) (*ContainerFile, error)
func (c *Client) DeleteContainerFile(context.Context, string, string) (*ContainerFileDeletion, error)
func (c *Client) DownloadContainerFile(context.Context, string, string, io.Writer) (*HTTPResponse, error)
```

| 类型 | 字段 |
| --- | --- |
| `AddContainerFileParams` | `FileID string`、`Extra map[string]any`；实现 `MarshalJSON` |
| `ListContainerFilesParams` | `After *string`、`Limit *int`、`Order *string` |
| `ContainerFile` | `ID`、`Object`、`ContainerID`、`Path`、`Source string`；`Bytes`、`CreatedAt int64`；`HTTP *HTTPResponse` |
| `ContainerFileList` | `Object`、`FirstID`、`LastID string`；`Data []ContainerFile`；`HasMore bool`；`HTTP *HTTPResponse` |
| `ContainerFileDeletion` | `ID`、`Object string`；`Deleted bool`；`HTTP *HTTPResponse` |

`AddContainerFile` 用 JSON 引用已有全局 `file_id`；`UploadContainerFile` 用 multipart 从 Reader 上传新文件。

```go
_, err = client.AddContainerFile(ctx, container.ID,
    openai.AddContainerFileParams{FileID: uploaded.ID})
files, err := client.ListContainerFiles(ctx, container.ID,
    openai.ListContainerFilesParams{Limit: openai.Ptr(100)})
if err != nil { return err }
out, err := os.Create("result.xlsx")
if err != nil { return err }
defer out.Close()
_, err = client.DownloadContainerFile(ctx, container.ID, files.Data[0].ID, out)
```

### Batches

```go
func (c *Client) CreateBatch(context.Context, CreateBatchParams) (*Batch, error)
func (c *Client) ListBatches(context.Context, ListBatchesParams) (*BatchList, error)
func (c *Client) GetBatch(context.Context, string) (*Batch, error)
func (c *Client) CancelBatch(context.Context, string) (*Batch, error)
```

| 类型 | 字段 |
| --- | --- |
| `CreateBatchParams` | `InputFileID`、`Endpoint`、`CompletionWindow string`；`Metadata map[string]string`；`OutputExpiresAfter *FileExpiration`；`Extra map[string]any`；实现 `MarshalJSON` |
| `ListBatchesParams` | `After *string`、`Limit *int` |
| `BatchErrorList` | `Object string`、`Data []BatchError` |
| `BatchError` | `Code`、`Message string`；`Line *int`、`Param *string` |
| `BatchRequestCounts` | `Total`、`Completed`、`Failed int` |
| `BatchUsage` | `InputTokens`、`OutputTokens`、`TotalTokens int`；`InputTokensDetails`、`OutputTokensDetails json.RawMessage` |
| `Batch` | ID/对象/输入/endpoint/window/status/model；创建与各阶段时间戳；errors；输出/错误文件 ID；请求计数；metadata；usage；`HTTP *HTTPResponse` |
| `BatchList` | `Object`、`FirstID`、`LastID string`；`Data []Batch`；`HasMore bool`；`HTTP *HTTPResponse` |

`Batch` 的完整字段为：`ID`、`Object`、`InputFileID`、`Endpoint`、`CompletionWindow`、`Status`、`Model string`，`CreatedAt int64`，`Errors *BatchErrorList`，`OutputFileID`、`ErrorFileID *string`，`InProgressAt`、`ExpiresAt`、`FinalizingAt`、`CompletedAt`、`FailedAt`、`ExpiredAt`、`CancellingAt`、`CancelledAt *int64`，`RequestCounts *BatchRequestCounts`，`Metadata map[string]string`，`Usage *BatchUsage`，`HTTP *HTTPResponse`。

Batch 的 `Endpoint` 直接传原生路径，例如 `/v1/responses` 或 `/v1/chat/completions`；客户端不将所有多模态接口视为支持 Batch，也不维护模型白名单。JSONL 每行使用官方 `custom_id`、`method`、`url`、`body` 格式，支持范围由服务端校验。

创建只提交作业，不轮询。目前官方 completion window 为 `24h`。`OutputExpiresAfter` 设置输出/错误文件到期策略。成功和失败记录均保持原始 JSONL，通过 `DownloadFile` 下载。

```go
batch, err := client.CreateBatch(ctx, openai.CreateBatchParams{
    InputFileID: uploaded.ID, Endpoint: "/v1/responses", CompletionWindow: "24h",
    Metadata: map[string]string{"job": "nightly"},
})
if err != nil { return err }
batch, err = client.GetBatch(ctx, batch.ID)
if err != nil { return err }
if batch.OutputFileID != nil {
    output, err := os.Create("batch-output.jsonl")
    if err != nil { return err }
    defer output.Close()
    _, err = client.DownloadFile(ctx, *batch.OutputFileID, output)
}
```

所有路径 ID 都按不透明单段处理并 URL 转义。空 ID、点段、斜线、反斜线、查询/片段分隔符、换行控制符和 NUL 会在发送请求前被拒绝。

完整的“上传 xlsx → 创建容器 → 分析 → 复用容器生成 pptx → 下载产物”示例见 [example_test.go](example_test.go)，该示例参与编译检查，不会在测试中调用云端。
