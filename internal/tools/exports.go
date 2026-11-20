package tools

import (
	"fmt"
	"net/url"
	"strings"
)

// 本文件是给自检与评测用的导出包装：把包内解析/转换能力暴露成稳定入口，
// 便于不依赖网络与外部工具链地验证行为。

// ParseTestReport 把测试输出解析成摘要文本（不含原始输出尾部），供自检校验解析规则。
func ParseTestReport(framework, out string, exitCode int) string {
	sum := parseTestOutput(framework, out, exitCode)
	var b strings.Builder
	fmt.Fprintf(&b, "结论: %s\n", map[bool]string{true: "失败", false: "通过"}[sum.Failed > 0])
	fmt.Fprintf(&b, "通过 %d / 失败 %d\n", sum.Passed, sum.Failed)
	for _, f := range sum.Failures {
		fmt.Fprintf(&b, "- %s %s\n", f.Name, f.Detail)
	}
	return b.String()
}

// ExtractWebText 把 HTML 转成正文，返回标题与正文。
func ExtractWebText(html string) (string, string) {
	return pageTitle(html), htmlToText(html)
}

// CheckFetchURL 校验一个地址是否允许抓取（仅 http/https 且公网可达目标）。
func CheckFetchURL(raw string) error {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("地址不合法: %v", err)
	}
	return checkPublicURL(u)
}
