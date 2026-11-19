package gemini

import (
	"testing"
)

// catalogWithNames 构造以指定官方名建目录的 payload，官方名行带 mode 与默认标记
func catalogWithNames(t *testing.T, namedRows map[string]int, defaultName string) ModelCatalog {
	t.Helper()
	rows := make([]any, 0, len(namedRows))
	hashes := map[string]string{}
	for name, mode := range namedRows {
		hash := "hash-" + name
		hashes[name] = hash
		row := make([]any, 18)
		row[0] = hash
		row[4] = float64(2)
		row[11] = name
		row[17] = float64(mode)
		rows = append(rows, any(row))
	}
	payload := make([]any, 16)
	payload[15] = rows
	// payload[0] 为默认 mode
	payload[0] = float64(namedRows[defaultName])
	catalog, err := parseModelCatalog(payload)
	if err != nil {
		t.Fatalf("parseModelCatalog: %v", err)
	}
	return catalog
}

func resolveOK(t *testing.T, catalog ModelCatalog, from, want string) {
	t.Helper()
	got, err := catalog.Resolve(from)
	if err != nil {
		t.Errorf("resolve %q: %v", from, err)
		return
	}
	if got.ID != want {
		t.Errorf("resolve %q = %q, want %q", from, got.ID, want)
	}
}

// TestLegacyAliasesFirstGeneration 数字命名 → 第一代模式命名
func TestLegacyAliasesFirstGeneration(t *testing.T) {
	catalog := catalogWithNames(t, map[string]int{
		"Quick answers":     6,
		"Everyday help":     1,
		"Most complex work": 8,
	}, "Everyday help")
	resolveOK(t, catalog, "gemini-3.5-flash-lite", "gemini-quick-answers")
	resolveOK(t, catalog, "gemini-3.8-flash", "gemini-everyday-help")
	resolveOK(t, catalog, "gemini-4-argon", "gemini-most-complex-work")
	resolveOK(t, catalog, "gemini-3.1-pro", "gemini-most-complex-work")
}

// TestLegacyAliasesSecondGeneration 第二代改名后（most-complex-work 缺席）别名应链到新名
func TestLegacyAliasesSecondGeneration(t *testing.T) {
	catalog := catalogWithNames(t, map[string]int{
		"Everyday help":       1,
		"Code complex work":   8,
		"Deeper analysis":     3,
	}, "Everyday help")
	resolveOK(t, catalog, "gemini-4-argon", "gemini-code-complex-work")
	resolveOK(t, catalog, "gemini-3.1-pro", "gemini-code-complex-work")
	// 第一代官方名本身也变成别名，链到第二代
	resolveOK(t, catalog, "gemini-most-complex-work", "gemini-code-complex-work")
	resolveOK(t, catalog, "gemini-quick-answers", "gemini-everyday-help")
	resolveOK(t, catalog, "gemini-detailed-research", "gemini-deeper-analysis")
	// 当前官方名不受影响
	resolveOK(t, catalog, "gemini-code-complex-work", "gemini-code-complex-work")
	resolveOK(t, catalog, "gemini-deeper-analysis", "gemini-deeper-analysis")
}

// TestLegacyAliasNeverShadowsOfficial 目录里若存在与别名同名的官方条目则不覆盖
func TestLegacyAliasNeverShadowsOfficial(t *testing.T) {
	// 假设未来官网重新提供名为 "Gemini 4 Argon" 的行：别名不得抢占
	catalog := catalogWithNames(t, map[string]int{
		"Everyday help":     1,
		"Code complex work": 8,
		"4 argon":           9,
	}, "Everyday help")
	got, err := catalog.Resolve("gemini-4-argon")
	if err != nil {
		t.Fatalf("resolve gemini-4-argon: %v", err)
	}
	if got.ID != "gemini-4-argon" {
		t.Fatalf("官方同名条目应优先，实际解析到 %q", got.ID)
	}
}
