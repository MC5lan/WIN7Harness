package agent

// 多模态（图片输入）支持：把本地图片路径转成 llm 消息里的 image_url 段。
// 兼顾老服务端：纯文本消息不产生 Parts，请求体与 v1.7 完全一致。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/llm"
)

// BuildUserMessage 构造一条可带图片的用户消息。images 为本地图片绝对路径。
// 返回的消息中 Content 为文本，Parts 为图片段。
func BuildUserMessage(text string, images []string) (llm.Message, error) {
	m := llm.Message{Role: "user", Content: text}
	if len(images) == 0 {
		return m, nil
	}
	if len(images) > core.MaxImagesPerMessage {
		images = images[:core.MaxImagesPerMessage]
	}
	var errs []string
	for _, p := range images {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			errs = append(errs, fmt.Sprintf("%s 不存在", filepath.Base(p)))
			continue
		}
		dataURL, err := core.ImageDataURL(p, core.MaxImageSide)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s 无法解析（%v）", filepath.Base(p), err))
			continue
		}
		m.Parts = append(m.Parts, llm.ImagePart(dataURL))
	}
	if len(m.Parts) == 0 {
		if len(errs) > 0 {
			return m, fmt.Errorf("图片未能附加：%s", strings.Join(errs, "；"))
		}
		return m, nil
	}
	return m, nil
}

// SendWithImages 发送一条带图片的用户消息（images 为本地图片路径，可为空）。
func (e *Engine) SendWithImages(userText string, images []string) {
	// 先做耗时检查/图片处理，最后在一个临界区内一次性完成“置 running + 建 cancel + 入队”，
	// 消除 Check-Then-Act 窗口：此前 Check(running) 与 set(running) 之间隔
	// 两段耗时操作，用户在此窗口内点“停止”会被静默吞掉
	// （e.cancel 仍是 nil，Stop() 找不到可取消对象）。
	msg, err := BuildUserMessage(userText, images)
	if err != nil {
		// 图片问题不阻塞文本发送，先提示再继续
		e.emit(Event{Kind: "error", Text: err.Error()})
	}
	ctx, cancel := context.WithCancel(context.Background())
	// 新的一轮用户请求：清掉上一轮"改过代码"的标记，
	// 否则纯问答也会触发自动验证，白跑一次构建。verifyMu 独立小锁，
	// 可在 e.mu 临界区外调用。
	e.resetWriteFlag()
	// 一次临界区完成：检查 running → 置位 → 挂 cancel → 追加消息。
	// 此前两次临界区之间隔着耗时操作（读图/编码），Stop() 在此窗口只会看到 cancel==nil，
	// 用户点“停止”被静默吞掉。
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		cancel()
		return
	}
	e.running = true
	e.cancel = cancel
	e.Messages = append(e.Messages, msg)
	e.mu.Unlock()

	e.runAsync(ctx)
}

// runAsync 在独立 goroutine 上跑 Agent 循环，负责收尾与 panic 兜底。
// Send 与 Retry 共用一份，避免两处收尾逻辑走偏。
func (e *Engine) runAsync(ctx context.Context) {
	core.SafeGo("agent/multimodal.go:86", func() {
		defer func() {
			e.mu.Lock()
			e.running = false
			e.cancel = nil
			e.mu.Unlock()
		}()
		// 引擎跑在自己的 goroutine 上，panic 会被 Go 直接抛成进程级崩溃。
		// 这里兜住：记录 + 落异常报告 + 给界面一个可读的错误，进程继续可用。
		defer func() {
			if r := recover(); r != nil {
				stack := string(debug.Stack())
				core.Logf("引擎 goroutine 崩溃: %v\n%s", r, stack)
				core.WriteCrashReport("engine-panic", fmt.Sprintf("%v\n\n%s", r, stack))
				e.emit(Event{Kind: "error", Err: true,
					Text: fmt.Sprintf("内部错误：%v（已写入异常报告，可用 /crash 查看）", r)})
			}
		}()
		e.loop(ctx)
	})
}

// hasImagesInContext 判断当前上下文里是否有图片（用于给出针对性的报错提示）。
func (e *Engine) hasImagesInContext() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.Messages) - 1; i >= 0 && i >= len(e.Messages)-6; i-- {
		if e.Messages[i].HasImages() {
			return true
		}
	}
	return false
}

// visionHint 当带图请求失败时，追加一条可操作的提示。
// 绝大多数模型已支持原生图片输入，但自建/老模型可能仍只接受纯文本。
func visionHint(err error) string {
	s := ""
	if err != nil {
		s = strings.ToLower(err.Error())
	}
	if !strings.Contains(s, "image") && !strings.Contains(s, "content") &&
		!strings.Contains(s, "400") && !strings.Contains(s, "unsupported") {
		return ""
	}
	return "\n\n提示：当前模型可能不支持图片输入。换一个支持视觉的模型后重试，或去掉图片只发文字。"
}
