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

// NormalizeStorageStatePayload 自动将各种 Cookie 格式（插件导出的数组或 Playwright 对象）转换为标准的 StorageState 字节
func NormalizeStorageStatePayload(existingPath string, inputData []byte) ([]byte, error) {
	var anyData interface{}
	if err := json.Unmarshal(inputData, &anyData); err != nil {
		return nil, fmt.Errorf("JSON 语法解析失败: %w", err)
	}

	// 读取原文件中的元数据（若存在）
	var oldMetadata interface{}
	if raw, err := os.ReadFile(existingPath); err == nil {
		var oldObj map[string]interface{}
		if json.Unmarshal(raw, &oldObj) == nil {
			oldMetadata = oldObj["geminiWeb2api"]
		}
	}

	// 1. 如果用户粘贴的是纯 Cookie 数组: [ {...}, {...} ]
	if arr, isArray := anyData.([]interface{}); isArray {
		convertedCookies := make([]map[string]interface{}, 0, len(arr))
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				c := map[string]interface{}{
					"name":     m["name"],
					"value":    m["value"],
					"domain":   m["domain"],
					"path":     m["path"],
					"httpOnly": m["httpOnly"],
					"secure":   m["secure"],
				}
				if exp, ok := m["expires"]; ok {
					c["expires"] = exp
				} else if expDate, ok := m["expirationDate"]; ok {
					c["expires"] = expDate
				} else {
					c["expires"] = -1
				}
				if sameSite, ok := m["sameSite"]; ok && sameSite != nil {
					c["sameSite"] = sameSite
				}
				convertedCookies = append(convertedCookies, c)
			}
		}
		resultObj := map[string]interface{}{
			"cookies": convertedCookies,
			"origins": []interface{}{},
		}
		if oldMetadata != nil {
			resultObj["geminiWeb2api"] = oldMetadata
		}
		return json.MarshalIndent(resultObj, "", "  ")
	}

	// 2. 如果是对象: { "cookies": [...], ... }
	if obj, isObj := anyData.(map[string]interface{}); isObj {
		if _, ok := obj["origins"]; !ok {
			obj["origins"] = []interface{}{}
		}
		if _, ok := obj["geminiWeb2api"]; !ok && oldMetadata != nil {
			obj["geminiWeb2api"] = oldMetadata
		}
		return json.MarshalIndent(obj, "", "  ")
	}

	return nil, errors.New("不支持的 JSON 结构，必须是 Cookie 对象或 Cookie 列表")
}

// WriteStorageStateRaw 校验 JSON 合法性，自动规格化后原子写回 storage-state.json
func WriteStorageStateRaw(path string, data []byte) error {
	if path == "" {
		return errors.New("路径不能为空")
	}

	normalized, err := NormalizeStorageStatePayload(path, data)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	return os.WriteFile(path, normalized, 0o600)
}
