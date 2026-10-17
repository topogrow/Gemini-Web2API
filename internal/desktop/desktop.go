//go:build desktop

// Package desktop 用标准桌面窗口与托盘承载管理端
package desktop

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Options 为桌面应用的运行参数
type Options struct {
	URL     string
	Title   string
	DataDir string
	Hidden  bool
	Serve   func(ctx context.Context) error
}

const HiddenFlag = "hidden"
const shellQuery = "shell"
const openPrefix = "desktop:open:"
const windowPrefix = "desktop:window:"
const maximisedEvent = "desktop:maximised"

var closeToTray = runtime.GOOS != "linux"

var windowActions = map[string]func(*application.WebviewWindow){
	"minimise":        func(window *application.WebviewWindow) { window.Minimise() },
	"toggle-maximise": (*application.WebviewWindow).ToggleMaximise,
	"close":           (*application.WebviewWindow).Close,
}

//go:embed icon.png
var icon []byte

//go:embed page.js
var pageScript string

var loadedEvents = []events.WindowEventType{
	events.Windows.WebViewNavigationCompleted,
	events.Mac.WebViewDidFinishNavigation,
	events.Linux.WindowLoadFinished,
}

// Run 启动桌面应用
func Run(options Options) error {
	page, err := windowURL(options.URL)
	if err != nil {
		return err
	}
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return err
	}

	var app *application.App
	var window *application.WebviewWindow
	show := func() {
		if window != nil {
			window.Show()
			window.UnMinimise()
			window.Focus()
		}
	}

	var sm *ServiceManager

	app = application.New(application.Options{
		Name: options.Title,
		Icon: icon,
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyRegular,
		},
		Windows: application.WindowsOptions{WebviewUserDataPath: dataDir},
		RawMessageHandler: func(_ application.Window, message string, _ *application.OriginInfo) {
			if target, ok := strings.CutPrefix(message, openPrefix); ok {
				openBrowser(app, target)
			} else if action, ok := strings.CutPrefix(message, windowPrefix); ok && windowActions[action] != nil {
				windowActions[action](window)
			} else if cmd, ok := strings.CutPrefix(message, "desktop:service:"); ok && sm != nil {
				switch cmd {
				case "start":
					_ = sm.StartService()
				case "stop":
					_ = sm.StopService()
				case "restart":
					_ = sm.RestartService()
				case "status":
					sendToWindow(window, "service_status", sm.GetStatus())
				case "logs":
					sendToWindow(window, "service_logs", sm.GetRecentLogs())
				}
			}
		},
	})

	defaultPort := 8007
	if p := page.Port(); p != "" {
		_, _ = fmt.Sscanf(p, "%d", &defaultPort)
	}
	sm = InitServiceManager(app, defaultPort, options.Serve)

	// 创建带原生标题栏与红绿灯控制的标准窗口
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            options.Title,
		URL:              page.String(),
		Frameless:        false,
		Hidden:           false,
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(0xf0, 0xf5, 0xf8),
	})
	window.Center()

	if closeToTray {
		window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
			window.Hide()
			event.Cancel()
		})
	}

	notifyMaximised := func(*application.WindowEvent) {
		window.ExecJS(fmt.Sprintf("window.dispatchEvent(new CustomEvent(%q,{detail:%t}))", maximisedEvent, window.IsMaximised()))
	}
	window.OnWindowEvent(events.Common.WindowDidResize, notifyMaximised)
	for _, loaded := range loadedEvents {
		window.OnWindowEvent(loaded, func(event *application.WindowEvent) {
			markReady(window)
			window.ExecJS(pageScript)
			notifyMaximised(event)
		})
	}
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { show() })

	menu := app.NewMenu()
	menu.Add("显示窗口").OnClick(func(*application.Context) { show() })
	menu.Add("在默认浏览器中打开").OnClick(func(*application.Context) { openBrowser(app, options.URL) })
	menu.Add("复制 API 地址").OnClick(func(*application.Context) {
		app.Clipboard.SetText(page.Scheme + "://" + page.Host)
	})
	menu.AddSeparator()
	menu.Add("重启 Web2API 服务").OnClick(func(*application.Context) {
		if sm != nil {
			_ = sm.RestartService()
		}
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) {
		if sm != nil {
			_ = sm.StopService()
		}
		app.Quit()
	})

	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip(options.Title)
	tray.SetMenu(menu)
	tray.OnClick(show)

	// 先在后台拉起服务，并等待端口接受连接
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = options.Serve(ctx)
	}()

	// 端口就绪等待（最多等待 3 秒）
	waitServing(page.Host, finished)

	// 启动后在主线程呈现窗口并呼起默认浏览器作为双保险
	app.Event.OnApplicationEvent(events.Mac.ApplicationDidFinishLaunching, func(*application.ApplicationEvent) {
		show()
		_ = app.Browser.OpenURL(options.URL)
	})
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		show()
	})

	err = app.Run()
	cancel()
	<-finished
	return err
}

func waitServing(address string, finished <-chan struct{}) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("tcp", address); err == nil {
			_ = conn.Close()
			return true
		}
		select {
		case <-finished:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}

func sendToWindow(window *application.WebviewWindow, eventName string, data interface{}) {
	if window == nil {
		return
	}
	window.ExecJS(fmt.Sprintf("window.dispatchEvent(new CustomEvent(%q, {detail: %s}))", "desktop:"+eventName, toJSONString(data)))
}

func toJSONString(data interface{}) string {
	bytes, err := json.Marshal(data)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

func windowURL(raw string) (*url.URL, error) {
	page, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	query := page.Query()
	query.Set(shellQuery, "desktop")
	page.RawQuery = query.Encode()
	return page, nil
}

func markReady(window *application.WebviewWindow) {
	window.HandleMessage("wails:runtime:ready")
}

func openBrowser(app *application.App, target string) {
	link, err := url.Parse(target)
	if err != nil || (link.Scheme != "http" && link.Scheme != "https") {
		return
	}
	if err := app.Browser.OpenURL(link.String()); err != nil {
		showError(app, err)
	}
}

func showError(app *application.App, err error) {
	if app != nil && app.Dialog != nil {
		app.Dialog.Error().SetTitle(app.Config().Name).SetMessage(err.Error()).Show()
	}
}
