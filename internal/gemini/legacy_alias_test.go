package gemini

import (
	"testing"
)

// TestLegacyAliasesResolve 旧数字命名模型 ID 应能解析到当前模式模型
func TestLegacyAliasesResolve(t *testing.T) {
	// 用 dump 测试验证过的真实行构造目录：mode=6 quick-answers, mode=1 everyday-help(默认), mode=8 most-complex-work
	rows := []any{
		[]any{"hashA", nil, nil, nil, float64(2), nil, nil, nil, nil, nil, nil, "Quick answers", nil, nil, nil, nil, nil, float64(6)},
		[]any{"hashB", nil, nil, nil, float64(2), nil, nil, nil, nil, nil, nil, "Everyday help", nil, nil, nil, nil, nil, float64(1)},
		[]any{"hashC", nil, nil, nil, float64(2), nil, nil, nil, nil, nil, nil, "Most complex work", nil, nil, nil, nil, nil, float64(8)},
	}
	payload := []any{float64(1), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, rows}
	catalog, err := parseModelCatalog(payload)
	if err != nil {
		t.Fatalf("parseModelCatalog: %v", err)
	}
	cases := map[string]string{
		"gemini-3.5-flash-lite": "gemini-quick-answers",
		"gemini-2.5-flash-lite": "gemini-quick-answers",
		"gemini-3.8-flash":      "gemini-everyday-help",
		"gemini-3.6-flash":      "gemini-everyday-help",
		"gemini-3.5-flash":      "gemini-everyday-help",
		"gemini":                "gemini-everyday-help",
		"gemini-3.1-pro":        "gemini-most-complex-work",
		"gemini-2.5-pro":        "gemini-most-complex-work",
		"gemini-pro":            "gemini-most-complex-work",
		// 当前官方名不受影响
		"gemini-quick-answers":     "gemini-quick-answers",
		"gemini-everyday-help":     "gemini-everyday-help",
		"gemini-most-complex-work": "gemini-most-complex-work",
	}
	for from, want := range cases {
		got, err := catalog.Resolve(from)
		if err != nil {
			t.Errorf("resolve %q: %v", from, err)
			continue
		}
		if got.ID != want {
			t.Errorf("resolve %q = %q, want %q", from, got.ID, want)
		}
	}
}
