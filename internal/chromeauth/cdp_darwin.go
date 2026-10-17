//go:build darwin

package chromeauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/bogdanfinn/websocket"
)

const defaultMacChromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

// MacChromeExecutable 返回本机可用的 Chrome 路径
func MacChromeExecutable() string {
	if path, err := exec.LookPath("google-chrome"); err == nil {
		return path
	}
	if _, err := os.Stat(defaultMacChromePath); err == nil {
		return defaultMacChromePath
	}
	return defaultMacChromePath
}

// AccountProfilePath 返回指定账号专用的独立隔离 Chrome Profile 路径（不自动创建目录）
func AccountProfilePath(accountID string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("定位用户配置目录: %w", err)
	}
	return filepath.Join(base, "Gemini-Web2API", "profiles", accountID), nil
}

// AccountProfileDir 返回指定账号专用的独立隔离 Chrome Profile 目录，并确保目录存在
func AccountProfileDir(accountID string) (string, error) {
	dir, err := AccountProfilePath(accountID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建账号 Profile 目录: %w", err)
	}
	return dir, nil
}

type cdpTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type cdpCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	Size     int     `json:"size"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	Session  bool    `json:"session"`
}

type cdpCookiesResult struct {
	ID     int `json:"id"`
	Result struct {
		Cookies []cdpCookie `json:"cookies"`
	} `json:"result"`
}

// RefreshDarwin 以 Headless 无头模式唤醒独立 Profile，访问 gemini.google.com 刷新 __Secure-1PSIDTS
func RefreshDarwin(ctx context.Context, accountID string, proxy string) ([]gemini.Cookie, error) {
	profileDir, err := AccountProfileDir(accountID)
	if err != nil {
		return nil, err
	}

	chromePath := MacChromeExecutable()
	if _, err := os.Stat(chromePath); err != nil {
		return nil, fmt.Errorf("未检测到本地 Google Chrome: %s", chromePath)
	}

	port := findFreePort(9222)
	logging.Info(accountID, "正在启动静默无头 Chrome 执行 CDP 续签...", "Profile", profileDir, "调试端口", port)

	args := []string{
		"--user-data-dir=" + profileDir,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
	}
	if proxy != "" {
		args = append(args, "--proxy-server="+proxy)
	}
	args = append(args, "https://gemini.google.com/app")

	cmdCtx, cmdCancel := context.WithCancel(ctx)
	defer cmdCancel()

	cmd := exec.CommandContext(cmdCtx, chromePath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 Chrome 失败: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// 轮询等待 Chrome 调试端口就绪并抓取 WebSocket 目标
	var wsURL string
	httpClient := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		resp, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var targets []cdpTarget
			if json.Unmarshal(body, &targets) == nil {
				for _, t := range targets {
					if t.Type == "page" && t.WebSocketDebuggerURL != "" {
						wsURL = t.WebSocketDebuggerURL
						break
					}
				}
			}
		}
		if wsURL != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if wsURL == "" {
		return nil, fmt.Errorf("Chrome 调试端点就绪超时")
	}

	// 给予 Google 前端 3 秒钟进行安全握手并写入滚动凭据
	time.Sleep(3500 * time.Millisecond)

	// 连接 CDP WebSocket 获取最新 Cookies
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 Chrome CDP 失败: %w", err)
	}
	defer conn.Close()

	getCookiesReq := map[string]interface{}{
		"id":     1,
		"method": "Network.getCookies",
		"params": map[string]interface{}{
			"urls": []string{"https://google.com", "https://gemini.google.com"},
		},
	}
	reqBytes, _ := json.Marshal(getCookiesReq)
	if err := conn.WriteMessage(websocket.TextMessage, reqBytes); err != nil {
		return nil, fmt.Errorf("发送 CDP 请求失败: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("读取 CDP 响应失败: %w", err)
	}

	var res cdpCookiesResult
	if err := json.Unmarshal(msg, &res); err != nil {
		return nil, fmt.Errorf("解析 CDP Cookies 响应失败: %w", err)
	}

	result := make([]gemini.Cookie, 0, len(res.Result.Cookies))
	hasSidts := false
	for _, c := range res.Result.Cookies {
		if c.Name == "__Secure-1PSIDTS" {
			hasSidts = true
		}
		result = append(result, gemini.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  int64(c.Expires),
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
		})
	}

	if !hasSidts {
		return nil, fmt.Errorf("未提取到最新的 __Secure-1PSIDTS，会话已彻底失效，请重新登录")
	}

	logging.Info(accountID, "macOS CDP 静默会话续签成功！", "提取Cookie数量", len(result))
	return result, nil
}

// LoginDarwin 唤起可见的 Chrome 独立窗口，供用户进行首次登录或重登，登录完成后自动提取 Cookies
func LoginDarwin(ctx context.Context, accountID string, proxy string) ([]gemini.Cookie, error) {
	profileDir, err := AccountProfileDir(accountID)
	if err != nil {
		return nil, err
	}

	chromePath := MacChromeExecutable()
	if _, err := os.Stat(chromePath); err != nil {
		return nil, fmt.Errorf("未检测到本地 Google Chrome: %s", chromePath)
	}

	port := findFreePort(9222)
	logging.Info(accountID, "已启动独立 Chrome 窗口，请在浏览器中完成登录...", "Profile", profileDir)

	args := []string{
		"--user-data-dir=" + profileDir,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1280,800",
	}
	if proxy != "" {
		args = append(args, "--proxy-server="+proxy)
	}
	args = append(args, "https://gemini.google.com/app")

	cmdCtx, cmdCancel := context.WithCancel(ctx)
	defer cmdCancel()

	cmd := exec.CommandContext(cmdCtx, chromePath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 Chrome 窗口失败: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	httpClient := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// 最长等待 10 分钟供用户扫码或输入密码登录
	deadline := time.Now().Add(10 * time.Minute)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		resp, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var targets []cdpTarget
		var wsURL string
		if json.Unmarshal(body, &targets) == nil {
			for _, t := range targets {
				if t.Type == "page" && t.WebSocketDebuggerURL != "" && strings.Contains(t.URL, "gemini.google.com") {
					wsURL = t.WebSocketDebuggerURL
					break
				}
			}
		}

		if wsURL == "" {
			continue
		}

		conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
		if err != nil {
			continue
		}

		getCookiesReq := map[string]interface{}{
			"id":     1,
			"method": "Network.getCookies",
			"params": map[string]interface{}{
				"urls": []string{"https://google.com", "https://gemini.google.com"},
			},
		}
		reqBytes, _ := json.Marshal(getCookiesReq)
		_ = conn.WriteMessage(websocket.TextMessage, reqBytes)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, msg, err := conn.ReadMessage()
		conn.Close()

		if err != nil {
			continue
		}

		var res cdpCookiesResult
		if json.Unmarshal(msg, &res) == nil {
			hasSidts := false
			for _, c := range res.Result.Cookies {
				if c.Name == "__Secure-1PSIDTS" {
					hasSidts = true
					break
				}
			}
			if hasSidts {
				result := make([]gemini.Cookie, 0, len(res.Result.Cookies))
				for _, c := range res.Result.Cookies {
					result = append(result, gemini.Cookie{
						Name:     c.Name,
						Value:    c.Value,
						Domain:   c.Domain,
						Path:     c.Path,
						Expires:  int64(c.Expires),
						HTTPOnly: c.HTTPOnly,
						Secure:   c.Secure,
					})
				}
				logging.Info(accountID, "检测到登录成功，Cookies 凭据已就绪！", "数量", len(result))
				return result, nil
			}
		}
	}

	return nil, fmt.Errorf("登录超时或未完成")
}

func findFreePort(start int) int {
	for p := start; p < start+200; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			_ = ln.Close()
			return p
		}
	}
	return start
}
