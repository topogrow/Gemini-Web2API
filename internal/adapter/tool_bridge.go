package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ToolDefinition 描述公开协议中的函数工具
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Namespace   string          `json:"-"`
}

// ToolCall 描述模型选择执行的函数工具
type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolBridge 将函数工具和结构化输出转换为严格文本契约
type ToolBridge struct {
	Definitions            []ToolDefinition
	Choice                 string
	JSONSchema             json.RawMessage
	CodeExecution          bool
	WebSearch              bool
	ImageGeneration        bool
	RequireCodeExecution   bool
	RequireWebSearch       bool
	RequireImageGeneration bool
	// AspectRatio 为生图请求的宽高比
	AspectRatio string
	// compactIndex 收纳按需裁剪时被降级的工具：PromptSuffix 只列出名称与用途，
	// 不再内联完整参数 schema；Definitions 保持全量，Parse 仍可校验与还原参数结构
	compactIndex []ToolDefinition
}

// promptCompactThreshold 是触发工具按需裁剪的完整 prompt 字节水位。
// 官网 StreamGenerate 在数 MB 时直接静默终止（见 gemini.maxPromptBytes），
// 约 2MB 起模型质量退化；超过该水位就把会话历史中未出现过的 MCP 工具
// 降级为名称索引，避免为用不到的 schema 烧掉预算。
const promptCompactThreshold = 1 << 20

// Compact 按会话历史裁剪工具声明：历史中出现过的工具与内置工具保留完整
// schema，其余 MCP 工具降级为轻量索引。transcript 为已渲染的会话文本，
// 工具名在其中出现即视为“本轮可能延续使用”。
func (b *ToolBridge) Compact(transcript string) {
	if len(b.Definitions) == 0 || len(b.compactIndex) > 0 {
		return
	}
	definitionsJSON, err := json.Marshal(b.Definitions)
	if err != nil {
		return
	}
	// 估算完整 prompt 规模（transcript + suffix 骨架），水位内不折腾
	if len(transcript)+len(definitionsJSON)+4096 < promptCompactThreshold {
		return
	}
	kept := make([]ToolDefinition, 0, len(b.Definitions))
	index := make([]ToolDefinition, 0)
	for _, definition := range b.Definitions {
		if isBuiltinToolName(definition.Name) || strings.Contains(transcript, definition.Name) {
			kept = append(kept, definition)
			continue
		}
		index = append(index, ToolDefinition{
			Name:        definition.Name,
			Description: firstSentence(definition.Description),
		})
	}
	// 全量都在历史里出现过时无需裁剪
	if len(index) == 0 {
		return
	}
	// Definitions 保持全量：Parse 仍可对被索引工具做参数结构校验；
	// 完整 schema 的序列化范围由 compactSchemaVisible 决定
	b.compactIndex = index
}

// isBuiltinToolName 判断是否为客户端内置工具（无 MCP server 前缀）。
// 内置工具（Bash、Read、Edit 等）是 agent 的基本操作面，永远保留完整 schema。
func isBuiltinToolName(name string) bool {
	return !strings.Contains(name, "__")
}

// firstSentence 截取描述首句，作为索引中的一行用途说明
func firstSentence(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return ""
	}
	if cut, _, found := strings.Cut(description, "。"); found && cut != "" {
		return cut + "。"
	}
	if cut, _, found := strings.Cut(description, ". "); found && cut != "" {
		return cut + "."
	}
	runes := []rune(description)
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return description
}

// PromptSuffix 返回追加到用户提示词的协议约束
func (b ToolBridge) PromptSuffix() (string, error) {
	var sections []string

	if len(b.Definitions) > 0 && !strings.EqualFold(strings.TrimSpace(b.Choice), "none") {
		visible := b.Definitions
		if len(b.compactIndex) > 0 {
			visible = b.compactSchemaVisible()
		}
		definitions, err := json.Marshal(visible)
		if err != nil {
			return "", fmt.Errorf("编码工具定义失败: %w", err)
		}

		choice := strings.TrimSpace(b.Choice)
		if choice == "" {
			choice = "auto"
		}
		rule := "需要调用工具时，只输出一个 JSON 对象；无需调用工具时，直接输出正常回答"
		switch strings.ToLower(choice) {
		case "required", "any":
			rule = "必须调用一个可用工具，只输出一个 JSON 对象"
		case "auto":
		default:
			rule = fmt.Sprintf("必须调用工具 %s，只输出一个 JSON 对象", choice)
		}

		sections = append(sections, fmt.Sprintf(`工具协议：
可用工具：%s
工具选择：%s
%s，格式必须为 {"tool_calls":[{"name":"工具名称","arguments":{}}]}，不要输出 Markdown 代码块或额外文字`, definitions, choice, rule))

		if len(b.compactIndex) > 0 {
			names := make([]string, 0, len(b.compactIndex))
			for _, definition := range b.compactIndex {
				entry := definition.Name
				if definition.Description != "" {
					entry += "（" + definition.Description + "）"
				}
				names = append(names, entry)
			}
			sections = append(sections, fmt.Sprintf(`另有 %d 个工具本次未列出参数结构，仅在确有需要时调用，参数按工具语义构造：%s`,
				len(b.compactIndex), strings.Join(names, "；")))
		}
	}

	if len(b.JSONSchema) > 0 {
		if _, err := compileJSONSchema(b.JSONSchema); err != nil {
			return "", fmt.Errorf("响应 JSON Schema 无效: %w", err)
		}
		sections = append(sections, fmt.Sprintf("结构化输出协议：只输出符合以下 JSON Schema 的 JSON 值，不要输出 Markdown 代码块或额外文字\n%s", b.JSONSchema))
	}

	if !strings.EqualFold(strings.TrimSpace(b.Choice), "none") {
		if b.CodeExecution {
			sections = append(sections, "本次请求可使用 Gemini Web 内置代码执行；涉及计算或运行代码时使用内置执行并返回实际结果")
		}
		if b.WebSearch {
			sections = append(sections, "本次请求可使用 Gemini Web 内置 Google 搜索；涉及实时或外部信息时搜索并保留来源")
		}
		if b.ImageGeneration {
			sections = append(sections, "本次请求可使用 Gemini Web 内置图片生成；仅在用户请求生成或编辑图片时使用")
		}
		if b.RequireCodeExecution {
			sections = append(sections, "本次请求必须使用 Gemini Web 内置代码执行")
		}
		if b.RequireWebSearch {
			sections = append(sections, "本次请求必须使用 Gemini Web 内置 Google 搜索并保留来源")
		}
		if b.RequireImageGeneration {
			sections = append(sections, "本次请求必须使用 Gemini Web 内置图片生成")
		}
	} else {
		sections = append(sections, "本次请求根据对话和已有工具结果直接回答；禁止调用函数工具、代码执行、Google 搜索和图片生成")
	}

	if len(sections) == 0 {
		return "", nil
	}
	return "\n\n" + strings.Join(sections, "\n\n"), nil
}

// Parse 解析严格工具调用并验证结构化输出
func (b ToolBridge) Parse(text string) (string, []ToolCall, error) {
	trimmed := stripJSONFence(strings.TrimSpace(text))

	toolJSON := extractToolCallJSON(trimmed)
	if len(b.Definitions) > 0 && toolJSON != "" {
		var envelope struct {
			ToolCalls []struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"tool_calls"`
		}
		if err := json.Unmarshal([]byte(toolJSON), &envelope); err == nil && len(envelope.ToolCalls) > 0 {
			if strings.EqualFold(strings.TrimSpace(b.Choice), "none") {
				return "", nil, fmt.Errorf("模型在 tool_choice=none 时返回了工具调用")
			}
			calls := make([]ToolCall, 0, len(envelope.ToolCalls))
			for index, call := range envelope.ToolCalls {
				if !b.hasTool(call.Name) {
					return "", nil, fmt.Errorf("模型返回了未声明的工具 %q", call.Name)
				}
				if b.explicitTool() != "" && call.Name != b.explicitTool() {
					return "", nil, fmt.Errorf("模型返回工具 %q，预期工具 %q", call.Name, b.explicitTool())
				}
				if len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
					return "", nil, fmt.Errorf("工具 %q 的 arguments 不是有效 JSON", call.Name)
				}
				definition := b.tool(call.Name)
				if err := validateJSONSchema(definition.Parameters, call.Arguments); err != nil {
					return "", nil, fmt.Errorf("工具 %q 的 arguments 不符合参数结构: %w", call.Name, err)
				}
				calls = append(calls, ToolCall{
					ID:        fmt.Sprintf("call_%d", index+1),
					Name:      call.Name,
					Arguments: call.Arguments,
				})
			}
			return "", calls, nil
		}
	}

	if b.requiresTool() {
		return "", nil, fmt.Errorf("模型没有返回必需的工具调用")
	}
	if len(b.JSONSchema) > 0 {
		if err := validateJSONSchema(b.JSONSchema, []byte(trimmed)); err != nil {
			head := []rune(trimmed)
			return "", nil, fmt.Errorf("模型返回内容不符合 JSON Schema: %w；返回内容开头：%q", err, string(head[:min(len(head), 200)]))
		}
		return trimmed, nil, nil
	}
	return text, nil, nil
}

func extractToolCallJSON(text string) string {
	if json.Valid([]byte(text)) {
		return text
	}
	for offset := 0; offset < len(text); {
		relative := strings.IndexByte(text[offset:], '{')
		if relative < 0 {
			return ""
		}
		start := offset + relative
		candidate := strings.TrimSpace(text[start:])
		candidate = strings.TrimSpace(strings.TrimSuffix(candidate, "```"))
		if json.Valid([]byte(candidate)) && strings.Contains(candidate, `"tool_calls"`) {
			return candidate
		}
		offset = start + 1
	}
	return ""
}

func (b ToolBridge) hasTool(name string) bool {
	return b.tool(name).Name != ""
}

// compactSchemaVisible 返回进入完整 schema 序列化的工具集合；
// 按需裁剪生效时只保留保留集，未裁剪时即全量
func (b ToolBridge) compactSchemaVisible() []ToolDefinition {
	if len(b.compactIndex) == 0 {
		return b.Definitions
	}
	hidden := make(map[string]struct{}, len(b.compactIndex))
	for _, definition := range b.compactIndex {
		hidden[definition.Name] = struct{}{}
	}
	visible := make([]ToolDefinition, 0, len(b.Definitions)-len(hidden))
	for _, definition := range b.Definitions {
		if _, ok := hidden[definition.Name]; !ok {
			visible = append(visible, definition)
		}
	}
	return visible
}

func (b ToolBridge) tool(name string) ToolDefinition {
	for _, definition := range b.Definitions {
		if definition.Name == name {
			return definition
		}
	}
	return ToolDefinition{}
}

func (b ToolBridge) requiresTool() bool {
	if len(b.Definitions) == 0 {
		return false
	}
	choice := strings.TrimSpace(strings.ToLower(b.Choice))
	return choice == "required" || (choice != "" && choice != "auto" && choice != "none")
}

func validateHostedOutput(output CandidateOutput, bridge ToolBridge) error {
	if bridge.disallowsHostedTools() {
		if len(output.Codes) > 0 || len(output.Citations) > 0 || hasGeneratedImage(output) {
			return fmt.Errorf("模型在 tool_choice=none 时使用了内置工具")
		}
		return nil
	}
	if bridge.RequireCodeExecution && len(output.Codes) == 0 {
		return fmt.Errorf("Gemini Web 未执行必需的代码工具")
	}
	if bridge.RequireWebSearch && len(output.Citations) == 0 {
		return fmt.Errorf("Gemini Web 未执行必需的搜索工具")
	}
	if bridge.RequireImageGeneration && !hasGeneratedImage(output) {
		return fmt.Errorf("Gemini Web 未执行必需的图片生成工具")
	}
	return nil
}

func (b ToolBridge) disallowsHostedTools() bool {
	return strings.EqualFold(strings.TrimSpace(b.Choice), "none")
}

func (b ToolBridge) explicitTool() string {
	choice := strings.TrimSpace(b.Choice)
	switch strings.ToLower(choice) {
	case "", "auto", "none", "required", "any":
		return ""
	default:
		return choice
	}
}

func stripJSONFence(text string) string {
	if !strings.HasPrefix(text, "```") || !strings.HasSuffix(text, "```") {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return text
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

func validateJSONSchema(schemaDocument json.RawMessage, instanceDocument []byte) error {
	if len(schemaDocument) == 0 {
		return nil
	}
	schema, err := compileJSONSchema(schemaDocument)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(instanceDocument))
	decoder.UseNumber()
	var instance interface{}
	if err := decoder.Decode(&instance); err != nil {
		return err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return err
	}
	return schema.Validate(instance)
}

func compileJSONSchema(schemaDocument json.RawMessage) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	schemaDecoder := json.NewDecoder(bytes.NewReader(schemaDocument))
	schemaDecoder.UseNumber()
	var schemaResource interface{}
	if err := schemaDecoder.Decode(&schemaResource); err != nil {
		return nil, err
	}
	if err := compiler.AddResource("schema.json", schemaResource); err != nil {
		return nil, err
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return nil, err
	}
	return schema, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra interface{}
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("JSON 包含多个顶层值")
	}
	return err
}

func assignToolCallIDs(calls []ToolCall, responseID string) []ToolCall {
	result := append([]ToolCall(nil), calls...)
	for index := range result {
		result[index].ID = fmt.Sprintf("call_%s_%d", responseID, index+1)
	}
	return result
}
