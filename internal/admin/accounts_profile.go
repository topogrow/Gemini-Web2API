package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
)

// CheckAccountProfile 检查该账号是否已经在本地建立了独立的 Chrome Profile 沙盒
func CheckAccountProfile(accountID string) (bool, string) {
	if accountID == "" {
		return false, ""
	}
	dir, err := chromeauth.AccountProfilePath(accountID)
	if err != nil {
		return false, ""
	}
	info, err := os.Stat(dir)
	if err == nil && info.IsDir() {
		return true, dir
	}
	return false, dir
}

// ResetAccountProfile 清空并重置该账号在本地的 Chrome Profile 目录
func ResetAccountProfile(accountID string) error {
	if accountID == "" {
		return nil
	}
	dir, err := chromeauth.AccountProfilePath(accountID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// ReadStorageStateRaw 读取指定路径的 storage-state.json 原始内容
func ReadStorageStateRaw(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("路径不能为空")
	}
	return os.ReadFile(path)
}

// WriteStorageStateRaw 校验 JSON 合法性并原子写回 storage-state.json
func WriteStorageStateRaw(path string, data []byte) error {
	if path == "" {
		return errors.New("路径不能为空")
	}
	var testMap map[string]interface{}
	if err := json.Unmarshal(data, &testMap); err != nil {
		return fmt.Errorf("JSON 格式不合法: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}
