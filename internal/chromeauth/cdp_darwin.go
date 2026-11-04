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
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/bogdanfinn/websocket"
)

func cleanupProfileLocks(dir string) {
	_ = os.Remove(filepath.Join(dir, "SingletonLock"))
	_ = os.Remove(filepath.Join(dir, "SingletonCookie"))
	_ = os.Remove(filepath.Join(dir, "SingletonSocket"))
}

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

type cdpCookieParam struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
	Expires  float64 `json:"expires,omitempty"`
}

func loadExistingStorageCookies(accountID string) ([]cdpCookieParam, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(base, "Gemini-Web2API", "auth", accountID, "storage-state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state struct {
		Cookies []struct {
			Name     string  `json:"name"`
			Value    string  `json:"value"`
			Domain   string  `json:"domain"`
			Path     string  `json:"path"`
			Expires  float64 `json:"expires"`
			HTTPOnly bool    `json:"httpOnly"`
			Secure   bool    `json:"secure"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	var params []cdpCookieParam
	for _, c := range state.Cookies {
		cp := cdpCookieParam{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
		}
		if c.Expires > 0 {
			cp.Expires = c.Expires
		}
		params = append(params, cp)
	}
	return params, nil
}

// RefreshDarwin 以 Headless 无头模式唤醒独立 Profile，访问 gemini.google.com 刷新 __Secure-1PSIDTS
func RefreshDarwin(ctx context.Context, accountID string, proxy string) ([]gemini.Cookie, error) {
	cdpRefreshLock.Lock()
	defer cdpRefreshLock.Unlock()

	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()

	profileDir, err := AccountProfileDir(accountID)
	if err != nil {
		return nil, err
	}

	chromePath := MacChromeExecutable()
	if _, err := os.Stat(chromePath); err != nil {
		return nil, fmt.Errorf("未检测到本地 Google Chrome: %s", chromePath)
	}

	existingCookies, _ := loadExistingStorageCookies(accountID)

	port := allocateDebugPort()
	logging.Info(accountID, "正在启动静默无头 Chrome 执行 CDP 续签...", "Profile", profileDir, "调试端口", port, "注入Cookie数", len(existingCookies))

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
	args = append(args, "about:blank")

	cleanupProfileLocks(profileDir)

	cmdCtx, cmdCancel := context.WithCancel(refreshCtx)
	defer cmdCancel()

	cmd := exec.CommandContext(cmdCtx, chromePath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 Chrome 失败: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	// 轮询等待 Chrome 调试端口就绪并抓取 WebSocket 目标
	var wsURL string
	httpClient := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-refreshCtx.Done():
			return nil, refreshCtx.Err()
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

	// 连接 CDP WebSocket
	conn, _, err := websocket.DefaultDialer.DialContext(refreshCtx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 Chrome CDP 失败: %w", err)
	}
	defer conn.Close()

	// 1. 若有存量 Cookie（如用户粘贴的 JSON），先向 Chrome 注入确保会话就绪
	if len(existingCookies) > 0 {
		setCookiesReq := map[string]interface{}{
			"id":     1,
			"method": "Network.setCookies",
			"params": map[string]interface{}{
				"cookies": existingCookies,
			},
		}
		reqBytes, _ := json.Marshal(setCookiesReq)
		_ = conn.WriteMessage(websocket.TextMessage, reqBytes)
		_, _ = readCdpMessageByID(conn, 1, 5*time.Second)
	}

	// 2. 导航至 https://gemini.google.com/app
	navReq := map[string]interface{}{
		"id":     2,
		"method": "Page.navigate",
		"params": map[string]interface{}{
			"url": "https://gemini.google.com/app",
		},
	}
	navBytes, _ := json.Marshal(navReq)
	_ = conn.WriteMessage(websocket.TextMessage, navBytes)
	_, _ = readCdpMessageByID(conn, 2, 5*time.Second)

	// 给予 Google 前端 4.5 秒钟进行页面渲染与 Cookie 滚动下发
	time.Sleep(4500 * time.Millisecond)

	// 3. 提取最新的 Cookies
	getCookiesReq := map[string]interface{}{
		"id":     3,
		"method": "Network.getCookies",
		"params": map[string]interface{}{
			"urls": []string{"https://google.com", "https://gemini.google.com", "https://accounts.google.com"},
		},
	}
	reqBytes, _ := json.Marshal(getCookiesReq)
	if err := conn.WriteMessage(websocket.TextMessage, reqBytes); err != nil {
		return nil, fmt.Errorf("发送 CDP 请求失败: %w", err)
	}

	msg, err := readCdpMessageByID(conn, 3, 10*time.Second)
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

	port := allocateDebugPort()
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

	cleanupProfileLocks(profileDir)

	cmdCtx, cmdCancel := context.WithCancel(ctx)
	defer cmdCancel()

	cmd := exec.CommandContext(cmdCtx, chromePath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 Chrome 窗口失败: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	chromeExited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(chromeExited)
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
		case <-chromeExited:
			return nil, fmt.Errorf("浏览器窗口已手动关闭，未完成登录")
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
				if t.Type == "page" && t.WebSocketDebuggerURL != "" &&
					strings.HasPrefix(t.URL, "https://gemini.google.com") &&
					!strings.Contains(t.URL, "accounts.google.com") &&
					!strings.Contains(t.URL, "sorry/index") {
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

		// 检查页面 DOM 中是否已载入登录态令牌 SNlM0e（证明真正登录成功并进入对话界面）
		evalReq := map[string]interface{}{
			"id": 10,
			"method": "Runtime.evaluate",
			"params": map[string]interface{}{
				"expression":    "document.documentElement.outerHTML.includes(\"SNlM0e\")",
				"returnByValue": true,
			},
		}
		evalBytes, _ := json.Marshal(evalReq)
		_ = conn.WriteMessage(websocket.TextMessage, evalBytes)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, evalMsg, err := conn.ReadMessage()
		if err != nil {
			conn.Close()
			continue
		}

		var evalRes struct {
			Result struct {
				Result struct {
					Value bool `json:"value"`
				} `json:"result"`
			} `json:"result"`
		}
		if json.Unmarshal(evalMsg, &evalRes) != nil || !evalRes.Result.Result.Value {
			conn.Close()
			continue
		}

		getCookiesReq := map[string]interface{}{
			"id":     1,
			"method": "Network.getCookies",
			"params": map[string]interface{}{
				"urls": []string{"https://google.com", "https://gemini.google.com", "https://accounts.google.com"},
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
				time.Sleep(1500 * time.Millisecond)
				logging.Info(accountID, "检测到登录成功，Cookies 凭据已就绪！", "数量", len(result))
				return result, nil
			}
		}
	}

		return nil, fmt.Errorf("登录超时或未完成")
	}

// SolveCaptchaDarwin 唤起可见 Chrome 独立窗口供用户进行人工滑块/人机验证，验证完成后自动提取包含 GOOGLE_ABUSE_EXEMPTION 在内的全量凭据
func SolveCaptchaDarwin(ctx context.Context, accountID string, proxy string) ([]gemini.Cookie, error) {
	profileDir, err := AccountProfileDir(accountID)
	if err != nil {
		return nil, err
	}

	chromePath := MacChromeExecutable()
	if _, err := os.Stat(chromePath); err != nil {
		return nil, fmt.Errorf("未检测到本地 Google Chrome: %s", chromePath)
	}

	existingCookies, _ := loadExistingStorageCookies(accountID)

	port := allocateDebugPort()
	logging.Info(accountID, "已启动独立 Chrome 窗口，请在浏览器中完成人机验证...", "Profile", profileDir, "注入Cookie数", len(existingCookies))

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
	args = append(args, "about:blank")

	cleanupProfileLocks(profileDir)

	cmdCtx, cmdCancel := context.WithCancel(ctx)
	defer cmdCancel()

	cmd := exec.CommandContext(cmdCtx, chromePath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 Chrome 窗口失败: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	chromeExited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(chromeExited)
	}()

	httpClient := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(10 * time.Minute)
	injected := false

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-chromeExited:
			return nil, fmt.Errorf("浏览器窗口已手动关闭，未完成验证")
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
				if t.Type == "page" && t.WebSocketDebuggerURL != "" {
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

		// 首次连接：注入已有 Cookies 并导航至 Gemini
		if !injected {
			if len(existingCookies) > 0 {
				setCookiesReq := map[string]interface{}{
					"id":     1,
					"method": "Network.setCookies",
					"params": map[string]interface{}{
						"cookies": existingCookies,
					},
				}
				reqBytes, _ := json.Marshal(setCookiesReq)
				_ = conn.WriteMessage(websocket.TextMessage, reqBytes)
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				_, _, _ = conn.ReadMessage()
			}

			navReq := map[string]interface{}{
				"id":     2,
				"method": "Page.navigate",
				"params": map[string]interface{}{
					"url": "https://gemini.google.com/app",
				},
			}
			navBytes, _ := json.Marshal(navReq)
			_ = conn.WriteMessage(websocket.TextMessage, navBytes)
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, _, _ = conn.ReadMessage()
			injected = true
			conn.Close()
			continue
		}

		// 已完成导航：检查页面是否脱离 sorry 拦截并进入 Gemini
		var onGeminiPage bool
		if json.Unmarshal(body, &targets) == nil {
			for _, t := range targets {
				if t.Type == "page" && strings.HasPrefix(t.URL, "https://gemini.google.com") &&
					!strings.Contains(t.URL, "accounts.google.com") &&
					!strings.Contains(t.URL, "sorry/index") {
					onGeminiPage = true
					break
				}
			}
		}

		if !onGeminiPage {
			conn.Close()
			continue
		}

		// 检查页面 DOM 中是否已载入登录态令牌 SNlM0e（证明真正通过验证并进入 Gemini 界面）
		evalReq := map[string]interface{}{
			"id": 10,
			"method": "Runtime.evaluate",
			"params": map[string]interface{}{
				"expression":    "document.documentElement.outerHTML.includes(\"SNlM0e\")",
				"returnByValue": true,
			},
		}
		evalBytes, _ := json.Marshal(evalReq)
		_ = conn.WriteMessage(websocket.TextMessage, evalBytes)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, evalMsg, err := conn.ReadMessage()
		if err != nil {
			conn.Close()
			continue
		}

		var evalRes struct {
			Result struct {
				Result struct {
					Value bool `json:"value"`
				} `json:"result"`
			} `json:"result"`
		}
		if json.Unmarshal(evalMsg, &evalRes) != nil || !evalRes.Result.Result.Value {
			conn.Close()
			continue
		}

		getCookiesReq := map[string]interface{}{
			"id":     3,
			"method": "Network.getCookies",
			"params": map[string]interface{}{
				"urls": []string{"https://google.com", "https://gemini.google.com", "https://accounts.google.com"},
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
				time.Sleep(1500 * time.Millisecond)
				logging.Info(accountID, "人工验证完成，已捕获最新免检 Cookies！", "数量", len(result))
				return result, nil
			}
		}
	}

	return nil, fmt.Errorf("人工验证超时或未完成")
}

var (
	cdpPortMu      sync.Mutex
	lastCdpPort    = 9222
	cdpRefreshLock sync.Mutex
)

func readCdpMessageByID(conn *websocket.Conn, targetID int, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	_ = conn.SetReadDeadline(deadline)
	for time.Now().Before(deadline) {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		var resp struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(msg, &resp) == nil && resp.ID == targetID {
			return msg, nil
		}
	}
	return nil, fmt.Errorf("CDP 响应等待超时 (id=%d)", targetID)
}

func allocateDebugPort() int {
	cdpPortMu.Lock()
	defer cdpPortMu.Unlock()
	for p := lastCdpPort; p < 9999; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			_ = ln.Close()
			lastCdpPort = p + 1
			if lastCdpPort >= 9990 {
				lastCdpPort = 9222
			}
			return p
		}
	}
	return 9222
}
