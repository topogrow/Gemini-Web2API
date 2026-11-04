//go:build darwin

package chromeauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/bogdanfinn/websocket"
)

func cleanupProfileLocksAndState(dir string) {
	if dir == "" {
		return
	}
	// 1. 清理根目录单例软链与版本锁
	_ = os.Remove(filepath.Join(dir, "SingletonLock"))
	_ = os.Remove(filepath.Join(dir, "SingletonCookie"))
	_ = os.Remove(filepath.Join(dir, "SingletonSocket"))
	_ = os.Remove(filepath.Join(dir, "RunningChromeVersion"))

	// 2. 递归查找并删除残留的 LevelDB LOCK 文件与 SQLite journal/wal 文件
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() {
			name := info.Name()
			if name == "LOCK" || strings.HasSuffix(name, "-journal") || strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") {
				_ = os.Remove(path)
			}
		}
		return nil
	})

	// 3. 修复 Default/Preferences 中的 exit_type 与 exited_cleanly，彻底消除 Chrome "打开您的个人资料时出了点问题" 弹窗
	prefPath := filepath.Join(dir, "Default", "Preferences")
	if data, err := os.ReadFile(prefPath); err == nil {
		var m map[string]interface{}
		if json.Unmarshal(data, &m) == nil {
			modified := false
			if prof, ok := m["profile"].(map[string]interface{}); ok {
				if prof["exit_type"] != "Normal" || prof["exited_cleanly"] != true {
					prof["exit_type"] = "Normal"
					prof["exited_cleanly"] = true
					modified = true
				}
			}
			if modified {
				if newData, err := json.Marshal(m); err == nil {
					_ = os.WriteFile(prefPath, newData, 0o600)
				}
			}
		}
	}

	// 4. 重置 Local State 中的 variations_crash_streak 计数
	localStatePath := filepath.Join(dir, "Local State")
	if data, err := os.ReadFile(localStatePath); err == nil {
		var m map[string]interface{}
		if json.Unmarshal(data, &m) == nil {
			if streak, ok := m["variations_crash_streak"].(float64); ok && streak > 0 {
				m["variations_crash_streak"] = 0
				if newData, err := json.Marshal(m); err == nil {
					_ = os.WriteFile(localStatePath, newData, 0o600)
				}
			}
		}
	}
}

func killProfileZombies(accountID string, dir string) {
	if accountID != "" {
		_ = exec.Command("pkill", "-9", "-f", "profiles/"+accountID).Run()
		_ = exec.Command("pkill", "-9", "-f", "--user-data-dir=.*"+accountID).Run()
	}
	if dir != "" {
		out, err := exec.Command("lsof", "-t", "+D", dir).Output()
		if err == nil && len(out) > 0 {
			pids := strings.Fields(string(out))
			for _, pidStr := range pids {
				var pid int
				if _, err := fmt.Sscanf(pidStr, "%d", &pid); err == nil && pid > 1 {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
		}
	}
	time.Sleep(100 * time.Millisecond)
	cleanupProfileLocksAndState(dir)
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

// interactiveSession 管理当前正在运行的可见独立 Chrome 窗口
type interactiveSession struct {
	AccountID     string
	ProfileDir    string
	Port          int
	Cmd           *exec.Cmd
	StartedAt     time.Time
	CloseChan     chan struct{}
	LatestCookies []gemini.Cookie
	mu            sync.RWMutex
}

var (
	interactiveMu       sync.Mutex
	interactiveSessions = make(map[string]*interactiveSession)
)

// IsInteractiveBrowserOpen 检查该账号是否正在打开独立的可视化 Chrome 窗口
func IsInteractiveBrowserOpen(accountID string) bool {
	interactiveMu.Lock()
	defer interactiveMu.Unlock()
	sess, ok := interactiveSessions[accountID]
	if !ok || sess == nil {
		return false
	}
	if sess.Cmd != nil && sess.Cmd.Process != nil {
		if err := sess.Cmd.Process.Signal(syscall.Signal(0)); err == nil {
			return true
		}
	}
	delete(interactiveSessions, accountID)
	return false
}

// launchInteractiveChrome 启动可视化独立 Chrome 窗口，并在后台长期运行，绝不主动 kill 窗口
func launchInteractiveChrome(accountID string, proxy string, initialURL string) (*interactiveSession, error) {
	interactiveMu.Lock()
	defer interactiveMu.Unlock()

	// 1. 如果已有活着的交互窗口，激活并返回
	if sess, ok := interactiveSessions[accountID]; ok && sess != nil {
		if sess.Cmd != nil && sess.Cmd.Process != nil && sess.Cmd.Process.Signal(syscall.Signal(0)) == nil {
			logging.Info(accountID, "独立 Chrome 窗口已在运行中，复用该窗口...")
			_ = exec.Command("osascript", "-e", `tell application "Google Chrome" to activate`).Run()
			return sess, nil
		}
		delete(interactiveSessions, accountID)
	}

	profileDir, err := AccountProfileDir(accountID)
	if err != nil {
		return nil, err
	}

	chromePath := MacChromeExecutable()
	if _, err := os.Stat(chromePath); err != nil {
		return nil, fmt.Errorf("未检测到本地 Google Chrome: %s", chromePath)
	}

	// 杀掉针对此账号目录的残留孤儿/僵尸进程并彻底清理文件锁与异常崩溃标记
	killProfileZombies(accountID, profileDir)

	port := allocateDebugPort()
	args := []string{
		"--user-data-dir=" + profileDir,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--no-first-run",
		"--no-default-browser-check",
		"--password-store=basic",
		"--disable-features=Translate,OptimizationGuideModelDownloading,OptimizationHints",
		"--disable-sync",
		"--window-size=1280,800",
	}
	if proxy != "" {
		args = append(args, "--proxy-server="+proxy)
	}
	if initialURL == "" {
		initialURL = "https://gemini.google.com/app"
	}
	args = append(args, initialURL)

	// 使用独立上下文启动 Chrome，脱离单次 HTTP 请求的生命周期！
	cmd := exec.Command(chromePath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 Chrome 窗口失败: %w", err)
	}

	sess := &interactiveSession{
		AccountID:  accountID,
		ProfileDir: profileDir,
		Port:       port,
		Cmd:        cmd,
		StartedAt:  time.Now(),
		CloseChan:  make(chan struct{}),
	}
	interactiveSessions[accountID] = sess

	// 监听进程自然退出（由用户手动关闭窗口触发）
	go func() {
		_ = cmd.Wait()
		interactiveMu.Lock()
		if interactiveSessions[accountID] == sess {
			delete(interactiveSessions, accountID)
		}
		interactiveMu.Unlock()
		close(sess.CloseChan)
		cleanupProfileLocksAndState(profileDir)
		logging.Info(accountID, "独立 Chrome 窗口已由用户手动关闭。")
	}()

	logging.Info(accountID, "已成功拉起独立 Chrome 窗口！", "Profile", profileDir, "调试端口", port)
	return sess, nil
}

// RefreshDarwin 以 Headless 无头模式唤醒独立 Profile，访问 gemini.google.com 刷新 __Secure-1PSIDTS
func RefreshDarwin(ctx context.Context, accountID string, proxy string) ([]gemini.Cookie, error) {
	cdpRefreshLock.Lock()
	defer cdpRefreshLock.Unlock()

	// 1. 如果该账号当前已经有可视化 Chrome 窗口在运行，直接从该窗口提取最新 Cookie，避免同时启动两个 Chrome 导致崩溃！
	if IsInteractiveBrowserOpen(accountID) {
		logging.Info(accountID, "检测到独立 Chrome 可视化窗口正在运行，直接从当前窗口提取最新 Cookie...")
		interactiveMu.Lock()
		sess := interactiveSessions[accountID]
		interactiveMu.Unlock()
		if sess != nil {
			cookies, err := fetchCookiesFromPort(sess.Port)
			if err == nil && len(cookies) > 0 {
				sess.mu.Lock()
				sess.LatestCookies = cookies
				sess.mu.Unlock()
				return cookies, nil
			}
		}
	}

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

		// 清理残留进程并释放锁与崩溃标记
		killProfileZombies(accountID, profileDir)

		port := allocateDebugPort()
		logging.Info(accountID, "正在启动静默无头 Chrome 执行 CDP 续签...", "Profile", profileDir, "调试端口", port, "注入Cookie数", len(existingCookies))

		args := []string{
			"--user-data-dir=" + profileDir,
			fmt.Sprintf("--remote-debugging-port=%d", port),
			"--headless=new",
			"--disable-gpu",
			"--no-first-run",
			"--no-default-browser-check",
			"--password-store=basic",
			"--disable-features=Translate,OptimizationGuideModelDownloading,OptimizationHints",
			"--disable-sync",
		}
		if proxy != "" {
			args = append(args, "--proxy-server="+proxy)
		}
		args = append(args, "about:blank")

		cmdCtx, cmdCancel := context.WithCancel(refreshCtx)
		defer cmdCancel()

		cmd := exec.CommandContext(cmdCtx, chromePath, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("启动 Chrome 失败: %w", err)
		}
		defer func() {
			if cmd.Process != nil {
				if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
				} else {
					_ = cmd.Process.Kill()
				}
				_ = cmd.Wait()
			}
			cleanupProfileLocksAndState(profileDir)
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

	// 1. 若有存量 Cookie，先注入确保会话就绪
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

// LoginDarwin 唤起可见的 Chrome 独立窗口供用户登录。检测到登录成功后提取 Cookies 并返回，但绝不关闭 Chrome 窗口！
func LoginDarwin(ctx context.Context, accountID string, proxy string) ([]gemini.Cookie, error) {
	sess, err := launchInteractiveChrome(accountID, proxy, "https://gemini.google.com/app")
	if err != nil {
		return nil, err
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// 最长等待 10 分钟供用户在窗口内扫码或输入密码登录
	deadline := time.Now().Add(10 * time.Minute)

	for time.Now().Before(deadline) {
		select {
		case <-sess.CloseChan:
			sess.mu.RLock()
			cookies := sess.LatestCookies
			sess.mu.RUnlock()
			if len(cookies) > 0 {
				return cookies, nil
			}
			return nil, fmt.Errorf("浏览器窗口已被手动关闭，未完成登录")
		case <-ctx.Done():
			// 即使 HTTP 请求断开，后台 Chrome 依然保持开启供用户操作
			sess.mu.RLock()
			cookies := sess.LatestCookies
			sess.mu.RUnlock()
			if len(cookies) > 0 {
				return cookies, nil
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}

		cookies, err := fetchCookiesFromPort(sess.Port)
		if err != nil || len(cookies) == 0 {
			continue
		}

		hasSidts := false
		for _, c := range cookies {
			if c.Name == "__Secure-1PSIDTS" {
				hasSidts = true
				break
			}
		}

		// 必须已经离开 Google 账号登录页 (accounts.google.com)，并位于 gemini.google.com
		if hasSidts && isGeminiAppReady(sess.Port) {
			sess.mu.Lock()
			sess.LatestCookies = cookies
			sess.mu.Unlock()

			logging.Info(accountID, "检测到登录成功，Cookies 凭据已就绪（Chrome 窗口保持开启供使用）！", "数量", len(cookies))
			return cookies, nil
		}
	}

	return nil, fmt.Errorf("登录超时或未完成")
}

// SolveCaptchaDarwin 唤起可见 Chrome 独立窗口供用户人工解除人机验证。验证完成后提取 Cookies 并返回，但绝不关闭 Chrome 窗口！
func SolveCaptchaDarwin(ctx context.Context, accountID string, proxy string) ([]gemini.Cookie, error) {
	existingCookies, _ := loadExistingStorageCookies(accountID)

	sess, err := launchInteractiveChrome(accountID, proxy, "about:blank")
	if err != nil {
		return nil, err
	}

	// 注入已有 Cookie 并导航到 gemini.google.com/app
	go func() {
		time.Sleep(1 * time.Second)
		injectCookiesAndNavigate(sess.Port, existingCookies, "https://gemini.google.com/app")
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(10 * time.Minute)

	for time.Now().Before(deadline) {
		select {
		case <-sess.CloseChan:
			sess.mu.RLock()
			cookies := sess.LatestCookies
			sess.mu.RUnlock()
			if len(cookies) > 0 {
				return cookies, nil
			}
			return nil, fmt.Errorf("验证窗口已被手动关闭，未完成验证")
		case <-ctx.Done():
			sess.mu.RLock()
			cookies := sess.LatestCookies
			sess.mu.RUnlock()
			if len(cookies) > 0 {
				return cookies, nil
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}

		cookies, err := fetchCookiesFromPort(sess.Port)
		if err != nil || len(cookies) == 0 {
			continue
		}

		hasAbuse := false
		hasSidts := false
		for _, c := range cookies {
			if c.Name == "GOOGLE_ABUSE_EXEMPTION" {
				hasAbuse = true
			}
			if c.Name == "__Secure-1PSIDTS" {
				hasSidts = true
			}
		}

		// 获得免检 Cookie 或成功进入 Gemini 主页
		if hasAbuse || (hasSidts && isGeminiAppReady(sess.Port)) {
			sess.mu.Lock()
			sess.LatestCookies = cookies
			sess.mu.Unlock()

			logging.Info(accountID, "人工验证完成，已捕获最新凭据（Chrome 窗口保持开启供使用）！", "数量", len(cookies))
			return cookies, nil
		}
	}

	return nil, fmt.Errorf("人工验证超时或未完成")
}

func fetchCookiesFromPort(port int) ([]gemini.Cookie, error) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var targets []cdpTarget
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, err
	}

	var wsURL string
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			wsURL = t.WebSocketDebuggerURL
			break
		}
	}
	if wsURL == "" {
		return nil, errors.New("未找到 page 目标")
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	getCookiesReq := map[string]interface{}{
		"id":     99,
		"method": "Network.getCookies",
		"params": map[string]interface{}{
			"urls": []string{"https://google.com", "https://gemini.google.com", "https://accounts.google.com"},
		},
	}
	reqBytes, _ := json.Marshal(getCookiesReq)
	if err := conn.WriteMessage(websocket.TextMessage, reqBytes); err != nil {
		return nil, err
	}

	msg, err := readCdpMessageByID(conn, 99, 3*time.Second)
	if err != nil {
		return nil, err
	}

	var res cdpCookiesResult
	if err := json.Unmarshal(msg, &res); err != nil {
		return nil, err
	}

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
	return result, nil
}

func isGeminiAppReady(port int) bool {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var targets []cdpTarget
	if json.Unmarshal(body, &targets) != nil {
		return false
	}
	for _, t := range targets {
		if t.Type == "page" {
			// 如果有 accounts.google.com 页面，说明还在登录页面
			if strings.Contains(t.URL, "accounts.google.com") {
				return false
			}
			if strings.HasPrefix(t.URL, "https://gemini.google.com") && !strings.Contains(t.URL, "sorry/index") {
				return true
			}
		}
	}
	return false
}

func injectCookiesAndNavigate(port int, cookies []cdpCookieParam, targetURL string) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var targets []cdpTarget
	if json.Unmarshal(body, &targets) != nil {
		return
	}

	var wsURL string
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			wsURL = t.WebSocketDebuggerURL
			break
		}
	}
	if wsURL == "" {
		return
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	if len(cookies) > 0 {
		setCookiesReq := map[string]interface{}{
			"id":     1,
			"method": "Network.setCookies",
			"params": map[string]interface{}{
				"cookies": cookies,
			},
		}
		reqBytes, _ := json.Marshal(setCookiesReq)
		_ = conn.WriteMessage(websocket.TextMessage, reqBytes)
		_, _ = readCdpMessageByID(conn, 1, 3*time.Second)
	}

	if targetURL != "" {
		navReq := map[string]interface{}{
			"id":     2,
			"method": "Page.navigate",
			"params": map[string]interface{}{
				"url": targetURL,
			},
		}
		navBytes, _ := json.Marshal(navReq)
		_ = conn.WriteMessage(websocket.TextMessage, navBytes)
		_, _ = readCdpMessageByID(conn, 2, 3*time.Second)
	}
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
