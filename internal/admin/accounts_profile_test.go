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

func TestAccountStorageState(t *testing.T) {
	testAccID := "test-storage-acc"
	tempDir, err := os.MkdirTemp("", "auth-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	statePath := filepath.Join(tempDir, testAccID, "storage-state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	initialJSON := `{
  "cookies": [
    {"name": "SID", "value": "test-sid", "domain": ".google.com", "path": "/"}
  ]
}`
	if err := os.WriteFile(statePath, []byte(initialJSON), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// 1. 测试读取
	raw, err := ReadStorageStateRaw(statePath)
	if err != nil {
		t.Fatalf("ReadStorageStateRaw failed: %v", err)
	}
	if string(raw) != initialJSON {
		t.Errorf("Expected raw=%s, got %s", initialJSON, string(raw))
	}

	// 2. 测试写入合法 JSON
	updatedJSON := `{
  "cookies": [
    {"name": "SID", "value": "updated-sid", "domain": ".google.com", "path": "/"},
    {"name": "__Secure-1PSIDTS", "value": "test-ts", "domain": ".google.com", "path": "/"}
  ]
}`
	err = WriteStorageStateRaw(statePath, []byte(updatedJSON))
	if err != nil {
		t.Fatalf("WriteStorageStateRaw failed: %v", err)
	}

	readBack, _ := ReadStorageStateRaw(statePath)
	if string(readBack) != updatedJSON {
		t.Errorf("Expected updatedJSON, got %s", string(readBack))
	}

	// 3. 测试写入非法 JSON
	err = WriteStorageStateRaw(statePath, []byte("{invalid-json"))
	if err == nil {
		t.Errorf("Expected error for invalid JSON, got nil")
	}
}

