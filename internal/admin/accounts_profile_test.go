package admin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
)

func TestAccountProfileHelpers(t *testing.T) {
	testAccID := "test-tdd-account"
	dir, err := chromeauth.AccountProfileDir(testAccID)
	if err != nil {
		t.Fatalf("AccountProfileDir failed: %v", err)
	}

	// 1. 刚创建或已存在目录测试
	has, profilePath := CheckAccountProfile(testAccID)
	if !has || profilePath != dir {
		t.Errorf("Expected has=true, got %v, path=%s", has, profilePath)
	}

	// 2. 清理/重置沙盒目录测试
	err = ResetAccountProfile(testAccID)
	if err != nil {
		t.Fatalf("ResetAccountProfile failed: %v", err)
	}

	// 验证重置后目录已不存在
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Expected dir to be removed, but still exists: %s", dir)
	}

	hasAfter, _ := CheckAccountProfile(testAccID)
	if hasAfter {
		t.Errorf("Expected hasAfter=false, got true")
	}

	// 清理临时文件
	_ = os.RemoveAll(filepath.Dir(dir))
}
