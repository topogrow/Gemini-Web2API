package admin

import (
	"os"

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
