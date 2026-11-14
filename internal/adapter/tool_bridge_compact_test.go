package adapter

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// 大工具集样例：模拟 MCP server 的详细 schema（真实 MCP 工具单个约 4KB）
func bigToolSet(mcpServers int, toolsPerServer int) []ToolDefinition {
	description := strings.Repeat("This tool performs a fairly complex operation with validation, dependency resolution and streaming results. ", 10)
	paramDoc := strings.Repeat("Parameter documentation. ", 15)
	properties := `{"path":{"type":"string","description":"` + paramDoc + `"},"content":{"type":"string","description":"` + paramDoc + `"},"options":{"type":"object","description":"` + paramDoc + `"},"mode":{"type":"string","description":"` + paramDoc + `"},"timeout":{"type":"number","description":"` + paramDoc + `"},"retries":{"type":"number","description":"` + paramDoc + `"},"tags":{"type":"array","description":"` + paramDoc + `"},"owner":{"type":"string","description":"` + paramDoc + `"},"branch":{"type":"string","description":"` + paramDoc + `"},"env":{"type":"object","description":"` + paramDoc + `"},"force":{"type":"boolean","description":"` + paramDoc + `"},"recursive":{"type":"boolean","description":"` + paramDoc + `"}}`
	parameters := json.RawMessage(`{"type":"object","properties":` + properties + `,"required":["path"]}`)
	definitions := make([]ToolDefinition, 0, mcpServers*toolsPerServer+2)
	for server := 0; server < mcpServers; server++ {
		for i := 0; i < toolsPerServer; i++ {
			definitions = append(definitions, ToolDefinition{
				Name:        "mcp__server" + string(rune('a'+server)) + "__tool_" + strconv.Itoa(i),
				Description: description,
				Parameters:  parameters,
			})
		}
	}
	definitions = append(definitions,
		ToolDefinition{Name: "Bash", Description: "Run shell commands", Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)},
		ToolDefinition{Name: "Read", Description: "Read files", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
	)
	return definitions
}

func mustJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// TestCompactBelowThreshold 小会话不裁剪
func TestCompactBelowThreshold(t *testing.T) {
	bridge := ToolBridge{Definitions: bigToolSet(2, 5)}
	small := strings.Repeat("短会话。", 10)
	bridge.Compact(small)
	if len(bridge.compactIndex) != 0 {
		t.Fatalf("低于水位不应裁剪，实际 %d 个进入索引", len(bridge.compactIndex))
	}
	suffix, err := bridge.PromptSuffix()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(suffix, "本次未列出参数结构") {
		t.Fatal("低于水位不应出现索引段")
	}
}

// TestCompactLargeSession 大会话裁剪：历史用过的保留，其余进索引
func TestCompactLargeSession(t *testing.T) {
	bridge := ToolBridge{Definitions: bigToolSet(20, 15)} // 302 个 MCP 工具 + 2 内置
	history := strings.Repeat("会话内容。", 20000)        // ~120KB 中文
	// 历史里调用过 servera 的 tool_0
	history += `\n{"tool_calls":[{"name":"mcp__servera__tool_0","arguments":{}}]}\n`
	bridge.Compact(history)

	if len(bridge.compactIndex) == 0 {
		t.Fatal("超过水位应发生裁剪")
	}
	total := len(bridge.Definitions)
	if len(bridge.compactIndex)+len(bridge.compactSchemaVisible()) != total {
		t.Fatalf("裁剪集合不一致: index=%d visible=%d total=%d", len(bridge.compactIndex), len(bridge.compactSchemaVisible()), total)
	}
	// 历史用过的必须保留完整 schema
	for _, visible := range bridge.compactSchemaVisible() {
		if visible.Name == "mcp__servera__tool_0" {
			if !strings.Contains(mustJSON(visible.Parameters), "Parameter documentation") {
				t.Fatal("历史使用过的工具参数 schema 应完整保留")
			}
		}
	}
	builtinKept := false
	for _, visible := range bridge.compactSchemaVisible() {
		if visible.Name == "Bash" {
			builtinKept = true
		}
	}
	if !builtinKept {
		t.Fatal("内置工具应保留完整 schema")
	}

	// PromptSuffix：索引工具不再出现在完整 JSON 中，但出现在索引段里
	suffix, err := bridge.PromptSuffix()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(suffix, "本次未列出参数结构") {
		t.Fatal("裁剪后应包含索引说明段")
	}
	// 索引段中的工具不应有完整 schema 重复
	visibleJSON := mustJSON(bridge.compactSchemaVisible())
	for _, hidden := range bridge.compactIndex {
		if strings.Contains(visibleJSON, `"`+hidden.Name+`"`) {
			t.Fatalf("被裁剪工具 %s 不应出现在完整 schema JSON", hidden.Name)
		}
		if !strings.Contains(suffix, hidden.Name) {
			t.Fatalf("被裁剪工具 %s 应回退为索引条目", hidden.Name)
		}
	}
}

// TestParseStillValidatesCompactedTools 裁剪后 Parse 仍校验全部声明工具
func TestParseStillValidatesCompactedTools(t *testing.T) {
	bridge := ToolBridge{Definitions: bigToolSet(20, 15)}
	history := strings.Repeat("会话内容。", 20000)
	bridge.Compact(history)
	if len(bridge.compactIndex) == 0 {
		t.Fatal("应发生裁剪")
	}
	// 模型调用一个被裁剪（仅索引）的工具：合法 JSON，应通过 hasTool 并按完整 schema 校验
	call := `{"tool_calls":[{"name":"mcp__servera__tool_3","arguments":{"path":"/tmp/x"}}]}`
	_, calls, err := bridge.Parse(call)
	if err != nil {
		t.Fatalf("索引内工具调用应被接受: %v", err)
	}
	if len(calls) != 1 || calls[0].Name != "mcp__servera__tool_3" {
		t.Fatalf("解析结果不符: %+v", calls)
	}
	// 未声明工具仍被拒绝
	if _, _, err := bridge.Parse(`{"tool_calls":[{"name":"nope","arguments":{}}]}`); err == nil {
		t.Fatal("未声明工具应被拒绝")
	}
}
