//go:build desktop

package desktop

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ServiceManager 负责后台 API 服务的生命周期管理，并暴露给前端 Wails 调用
type ServiceManager struct {
	mu          sync.Mutex
	running     bool
	currentPort int
	app         *application.App
	cancelFunc  context.CancelFunc
	serverFn    func(ctx context.Context) error
	finished    chan struct{}

	logBuffer []string
	logMu     sync.RWMutex
}

var globalManager *ServiceManager

// InitServiceManager 初始化全局服务管理器
func InitServiceManager(app *application.App, defaultPort int, serverFn func(ctx context.Context) error) *ServiceManager {
	globalManager = &ServiceManager{
		app:         app,
		currentPort: defaultPort,
		serverFn:    serverFn,
		logBuffer:   make([]string, 0, 500),
	}
	return globalManager
}

// GetServiceManager 获取全局实例
func GetServiceManager() *ServiceManager {
	return globalManager
}

// CheckPort 检查目标端口是否可用
func (sm *ServiceManager) CheckPort(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// StartService 启动后台服务
func (sm *ServiceManager) StartService() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.running {
		return fmt.Errorf("服务已经在运行中")
	}

	ctx, cancel := context.WithCancel(context.Background())
	sm.cancelFunc = cancel
	sm.finished = make(chan struct{})
	sm.running = true

	go func() {
		defer close(sm.finished)
		sm.AppendLog(fmt.Sprintf("正在拉起 Web2API 核心服务 (端口: %d)...", sm.currentPort))
		err := sm.serverFn(ctx)
		sm.mu.Lock()
		sm.running = false
		sm.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			sm.AppendLog(fmt.Sprintf("服务运行异常退出: %v", err))
		} else {
			sm.AppendLog("服务已正常停止")
		}
		sm.notifyStateChange()
	}()

	sm.notifyStateChange()
	return nil
}

// StopService 停止后台服务
func (sm *ServiceManager) StopService() error {
	sm.mu.Lock()
	if !sm.running {
		sm.mu.Unlock()
		return nil
	}
	sm.AppendLog("收到停止指令，正在关闭后台 Web2API 服务...")
	if sm.cancelFunc != nil {
		sm.cancelFunc()
	}
	finished := sm.finished
	sm.mu.Unlock()

	if finished != nil {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			sm.AppendLog("等待服务退出超时")
		}
	}
	return nil
}

// RestartService 一键重启服务
func (sm *ServiceManager) RestartService() error {
	_ = sm.StopService()
	time.Sleep(500 * time.Millisecond)
	return sm.StartService()
}

// AppendLog 追加日志并推送到桌面前端
func (sm *ServiceManager) AppendLog(line string) {
	entry := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), line)
	sm.logMu.Lock()
	if len(sm.logBuffer) >= 500 {
		sm.logBuffer = sm.logBuffer[1:]
	}
	sm.logBuffer = append(sm.logBuffer, entry)
	sm.logMu.Unlock()

	if sm.app != nil {
		go sm.app.Event.Emit("desktop:service_log", entry)
	}
}

// GetRecentLogs 返回最近的日志
func (sm *ServiceManager) GetRecentLogs() []string {
	sm.logMu.RLock()
	defer sm.logMu.RUnlock()
	copied := make([]string, len(sm.logBuffer))
	copy(copied, sm.logBuffer)
	return copied
}

// GetStatus 获取当前运行状态
func (sm *ServiceManager) GetStatus() map[string]interface{} {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return map[string]interface{}{
		"running": sm.running,
		"port":    sm.currentPort,
	}
}

func (sm *ServiceManager) notifyStateChange() {
	if sm.app != nil {
		go sm.app.Event.Emit("desktop:service_status", sm.GetStatus())
	}
}
