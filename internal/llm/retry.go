package llm

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 网络抖动、端点限流、上游 5xx 都不该让一整轮任务直接作废：
// 这里做有限次指数退避重试，只在"还没开始出内容"的阶段生效。
const (
	defaultRetryBase = time.Second
	maxRetryDelay    = 8 * time.Second
)

// retryableStatus 判断状态码是否值得重试。
// 408 请求超时 / 425 过早 / 429 限流 / 5xx 上游故障（含 Cloudflare 529）。
func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, 425, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	}
	return false
}

// retryableNetErr 判断传输层错误是否值得重试。
// 上下文被用户取消或整体超时的不重试。
// x509 证书类错误不重试：重试只会撞同一堵墙，白白拖慢诊断。
func retryableNetErr(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if isX509Error(err) {
		return false
	}
	return true
}

// isX509Error 识别证书链校验类错误（含 fmt 包装）。
func isX509Error(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "x509") || strings.Contains(msg, "certificate") ||
		strings.Contains(msg, "certificate signed by unknown authority") ||
		strings.Contains(msg, "certificate is either not yet valid")
}

// retryAfter 解析 Retry-After 头（秒数或 HTTP-date 形式），
// 上限取 max(maxRetryDelay, 30s)——服务端要更久就别硬顶着重试。
func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		d := time.Duration(n) * time.Second
		if d > 60*time.Second {
			d = 60 * time.Second
		}
		return d
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		if d > 60*time.Second {
			d = 60 * time.Second
		}
		return d
	}
	return 0
}

// backoff 第 attempt 次重试的等待时长：base, 2*base, 4*base…
// 上限 maxRetryDelay，并加 ±25% 抖动避免多数实例同时重试（羊群效应）；
// 抖动后仍保证不超过封顶值。
func backoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = defaultRetryBase
	}
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= maxRetryDelay {
			d = maxRetryDelay
			break
		}
	}
	// 抖动：±25%
	jitter := float64(d) * 0.25 * (2*rand.Float64() - 1)
	d = time.Duration(float64(d) + jitter)
	if d > maxRetryDelay {
		d = maxRetryDelay
	}
	if d < 0 {
		d = 0
	}
	return d
}

// sleepCtx 等待 d，期间上下文结束则返回 false。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// friendlyNetErr 把底层网络错误翻成用户看得懂、能动手的方向。
// Win7 上绝大多数"连不上"就三类：DNS、被墙/防火墙、证书链不全。
func friendlyNetErr(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	var hint string
	switch {
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "x509"):
		hint = "证书链不受信任：Win7 系统根证书库停在 2009 年左右，程序内置 22 张现代根证书兜底——若端点证书到的根不在二者内，请在设置页填 TLS ExtraCA；若被公司代理/杀软 MITM，需把它的根 CA 加进去；别去装 KB4474419（纯 Go TLS 不走 Schannel，装了也无用）"
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "server misbehaving"):
		hint = "域名解析失败：检查网络、DNS 或代理设置"
	case strings.Contains(msg, "connection refused"):
		hint = "连接被拒绝：本地模型未启动，或代理/防火墙拦截了该端口"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		hint = "连接超时：网络不通或代理未生效"
	case strings.Contains(msg, "eof") || strings.Contains(msg, "reset by peer"):
		hint = "连接被中断：多为网络不稳或中间设备掐断，可重试一次"
	}
	if hint == "" {
		return err
	}
	return fmt.Errorf("%s（%v）", hint, err)
}
