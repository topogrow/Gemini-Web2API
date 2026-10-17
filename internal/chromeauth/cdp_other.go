//go:build !darwin

package chromeauth

import (
	"context"
	"fmt"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

func AccountProfilePath(accountID string) (string, error) {
	return "", fmt.Errorf("Chrome Profile 仅在 macOS 上生效")
}

func AccountProfileDir(accountID string) (string, error) {
	return "", fmt.Errorf("Chrome Profile 仅在 macOS 上生效")
}

func RefreshDarwin(ctx context.Context, accountID string, proxy string) ([]gemini.Cookie, error) {
	return nil, fmt.Errorf("CDP 自动续签仅在 macOS 上生效")
}

func LoginDarwin(ctx context.Context, accountID string, proxy string) ([]gemini.Cookie, error) {
	return nil, fmt.Errorf("CDP 登录仅在 macOS 上生效")
}
