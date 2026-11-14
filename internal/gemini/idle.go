package gemini

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// requestCeilingSeconds 是单个上游 HTTP 请求的总时限上限
const requestCeilingSeconds = 3600

// rpcTimeout 是单个 batchexecute RPC 的时限
const rpcTimeout = 600 * time.Second

// maxPromptBytes 是官网 StreamGenerate 能稳定接受的提示词字节上限。
// 实测约 2MB 起模型质量退化（复读拒答），数 MB 时上游直接回终止帧且不给候选；
// 取 2MB 作为前置拦截水位，超限返回 413 而不是把账号拖进无意义的冷却。
const maxPromptBytes = 2 << 20

// streamIdleTimeout 是生成流距上一个语义事件的最长等待；上游心跳同样算作进展，
// 因此它实际衡量的是「连接彻底静默」的时长，而不是「多久没出正文」。
// 重度档位（Most complex work / 原 3.1 Pro、4 Argon）的思考阶段可能长达数分钟，
// 期间只有心跳，不能被这里误杀。
var streamIdleTimeout = 600 * time.Second

// errStreamIdle 表示生成流长时间没有语义事件
var errStreamIdle = errors.New("gemini web stream idle")

// idleKey 是生成流上下文中最近进展时间的键
type idleKey struct{}

// watchStreamIdle 返回在 streamIdleTimeout 内没有进展时取消的上下文
func watchStreamIdle(parent context.Context) (context.Context, func()) {
	last := &atomic.Int64{}
	last.Store(time.Now().UnixNano())
	ctx, cancel := context.WithCancelCause(context.WithValue(parent, idleKey{}, last))
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if now.Sub(time.Unix(0, last.Load())) >= streamIdleTimeout {
					cancel(errStreamIdle)
					return
				}
			}
		}
	}()
	return ctx, func() {
		close(done)
		cancel(nil)
	}
}

// progress 记录生成流的一次进展
func progress(ctx context.Context) {
	if last, ok := ctx.Value(idleKey{}).(*atomic.Int64); ok {
		last.Store(time.Now().UnixNano())
	}
}

// streamIdleError 在生成流因长时间没有进展被取消时返回可换号重试的上游错误
func streamIdleError(ctx context.Context) error {
	if !errors.Is(context.Cause(ctx), errStreamIdle) {
		return nil
	}
	return &ProtocolError{
		HTTPStatus: 504, Code: 504, Retryable: true, Cause: errStreamIdle,
		Message: fmt.Sprintf("Gemini Web 生成流 %s 没有新内容，只有心跳", streamIdleTimeout),
	}
}
