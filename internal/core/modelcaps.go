package core

import "strings"

// ModelCapabilities 是依据模型名/端点推测的保守能力提示。
// 只用于 UI 建议，不自动覆盖用户配置：猜错窗口比填小一点更危险。
type ModelCapabilities struct {
	ContextWindow int
	MaxOutput     int
	Source        string
}

// SuggestModelCapabilities 按常见模型名给出保守建议。
// 返回 0 表示没有可靠线索，调用方应继续用手动窗口或 128k 兜底。
func SuggestModelCapabilities(baseURL, model string) ModelCapabilities {
	lowModel := strings.ToLower(strings.TrimSpace(model))
	lowBase := strings.ToLower(strings.TrimSpace(baseURL))
	if lowModel == "" {
		return ModelCapabilities{}
	}
	switch {
	case strings.Contains(lowModel, "gemini"):
		return ModelCapabilities{ContextWindow: 1_000_000, MaxOutput: 8192, Source: "Gemini 系列常见 1M"}
	case strings.Contains(lowModel, "claude"):
		return ModelCapabilities{ContextWindow: 200_000, MaxOutput: 8192, Source: "Claude 系列常见 200k"}
	case strings.Contains(lowModel, "kimi") || strings.Contains(lowModel, "moonshot"):
		return ModelCapabilities{ContextWindow: 256_000, MaxOutput: 8192, Source: "Kimi/Moonshot 常见 256k"}
	case strings.Contains(lowModel, "qwen") || strings.Contains(lowModel, "qwq"):
		return ModelCapabilities{ContextWindow: 131_072, MaxOutput: 8192, Source: "Qwen 系列常见 128k"}
	case strings.Contains(lowModel, "deepseek"):
		return ModelCapabilities{ContextWindow: 128_000, MaxOutput: 8192, Source: "DeepSeek 系列常见 128k"}
	case strings.Contains(lowModel, "glm"):
		return ModelCapabilities{ContextWindow: 128_000, MaxOutput: 8192, Source: "GLM 系列常见 128k"}
	case strings.Contains(lowModel, "gpt-4.1") || strings.Contains(lowModel, "gpt-5"):
		return ModelCapabilities{ContextWindow: 1_000_000, MaxOutput: 16384, Source: "GPT-4.1/5 系列常见 1M"}
	case strings.Contains(lowModel, "gpt-4o") || strings.Contains(lowModel, "gpt-4"):
		return ModelCapabilities{ContextWindow: 128_000, MaxOutput: 8192, Source: "GPT-4 系列常见 128k"}
	case strings.Contains(lowBase, "11434") || strings.Contains(lowBase, "1234") || strings.Contains(lowModel, "ollama"):
		return ModelCapabilities{ContextWindow: 32_768, MaxOutput: 4096, Source: "本地模型保守按 32k"}
	}
	return ModelCapabilities{}
}
