package balancer

import (
	"testing"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

func TestAccountPool_ClearCooldown(t *testing.T) {
	p := NewAccountPool(2*time.Minute, 10*time.Minute)
	accountID := "test-acc-captcha"
	p.Register(accountID, AccountInfo{Source: "cookie"}, false)

	// 模拟遭遇人机验证拦截并报告失败
	p.ReportFailure(accountID, "", gemini.ErrCaptcha)

	entry := p.entry(accountID)
	if entry == nil {
		t.Fatalf("entry not found")
	}
	if entry.Cooldown.reason != "captcha" {
		t.Errorf("expected cooldown reason 'captcha', got '%s'", entry.Cooldown.reason)
	}
	if entry.LastError == "" {
		t.Errorf("expected LastError to be set")
	}

	// 执行清除冷却
	p.ClearCooldown(accountID)

	if entry.Cooldown.reason != "" {
		t.Errorf("expected cooldown to be cleared, got '%s'", entry.Cooldown.reason)
	}
	if entry.LastError != "" {
		t.Errorf("expected LastError to be empty, got '%s'", entry.LastError)
	}
	if len(entry.ModelCooldowns) != 0 {
		t.Errorf("expected ModelCooldowns to be empty")
	}
}
