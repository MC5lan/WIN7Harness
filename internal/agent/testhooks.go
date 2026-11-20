package agent

// 自检专用的导出包装。
//
// 这些函数不参与运行时逻辑，只把内部构造与用量累计暴露给 internal/selftest，
// 让自检能造出"两轮带缓存命中"的真实数据来验证成本看板，而不是断言函数存在。

import "win7harness/internal/llm"

// NewTestEngine 构造一个不联网的引擎，仅用于自检与单测。
func NewTestEngine(workDir string) *Engine {
	e := New(nil, "selftest-model", workDir)
	return e
}

// RecordUsageForTest 按一轮调用的真实形态累计用量（输入/命中/输出）。
func (e *Engine) RecordUsageForTest(model string, prompt, cached, completion int) {
	e.Model = model
	e.addUsage(llm.Usage{
		PromptTokens:     prompt,
		CachedTokens:     cached,
		CompletionTokens: completion,
	})
}

// RecordModelUsageForTest 按"某模型实际回了一轮"记账，走 addUsageFor 的分桶路径。
// 与 RecordUsageForTest 的区别：不篡改 e.Model，能真实验证主/快速两个账本是否分开。
func (e *Engine) RecordModelUsageForTest(model string, prompt, cached, completion int) {
	e.addUsageFor(model, llm.Usage{
		PromptTokens:     prompt,
		CachedTokens:     cached,
		CompletionTokens: completion,
	})
}

// ClipSubResultForTest 暴露子代理结论裁剪逻辑，供自检用真实超长文本验证预算生效。
func ClipSubResultForTest(s string, max int) string { return clipSubResult(s, max) }

// FastUsageForTest 暴露快速模型分桶用量，供自检验证两个账本没有互相污染。
func (e *Engine) FastUsageForTest() (int, int, int) { return e.fastUsage() }
