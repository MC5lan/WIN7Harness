package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Comparison 是一次评测结果与基线的逐用例对比。
// 自迭代门禁关心的是"有没有退步"，不是"是不是满分"：基线 100% 时，
// 只看总分永远看不出候选改坏了哪一条。
type Comparison struct {
	BaseScore    float64  `json:"base_score"`
	CurrentScore float64  `json:"current_score"`
	Regressions  []string `json:"regressions,omitempty"`
	Improvements []string `json:"improvements,omitempty"`
	Added        []string `json:"added,omitempty"`
	Missing      []string `json:"missing,omitempty"`
}

// OK 表示没有退步、也没有用例消失。
// 新增用例不算退步；跳过按失败处理（StatusSkip 不是 pass）。
func (c Comparison) OK() bool {
	return len(c.Regressions) == 0 && len(c.Missing) == 0
}

// Compare 按用例 ID 对比基线与当前结果。
func Compare(base, cur Report) Comparison {
	c := Comparison{
		BaseScore:    base.Score(),
		CurrentScore: cur.Score(),
	}
	baseByID := make(map[string]Result, len(base.Results))
	for _, r := range base.Results {
		baseByID[r.ID] = r
	}
	seen := make(map[string]bool, len(cur.Results))
	for _, r := range cur.Results {
		seen[r.ID] = true
		b, ok := baseByID[r.ID]
		if !ok {
			c.Added = append(c.Added, r.ID)
			continue
		}
		switch {
		case b.Status == StatusPass && r.Status != StatusPass:
			c.Regressions = append(c.Regressions, r.ID)
		case b.Status != StatusPass && r.Status == StatusPass:
			c.Improvements = append(c.Improvements, r.ID)
		}
	}
	for _, r := range base.Results {
		if !seen[r.ID] {
			c.Missing = append(c.Missing, r.ID)
		}
	}
	sort.Strings(c.Regressions)
	sort.Strings(c.Improvements)
	sort.Strings(c.Added)
	sort.Strings(c.Missing)
	return c
}

// LoadReport 读取 evalrun 落盘的 JSON 报告，供 CI / 门禁做基线对比。
func LoadReport(path string) (Report, error) {
	var r Report
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("解析基线报告失败：%w", err)
	}
	return r, nil
}

// Markdown 生成对比报告。
func (c Comparison) Markdown() string {
	var b strings.Builder
	b.WriteString("# 评测基线对比\n\n")
	fmt.Fprintf(&b, "- 基线通过率：%.1f%%\n- 当前通过率：%.1f%%\n", c.BaseScore*100, c.CurrentScore*100)
	if c.OK() {
		b.WriteString("- 结论：无退步\n")
	} else {
		b.WriteString("- 结论：**有退步**\n")
	}
	if len(c.Regressions) > 0 {
		b.WriteString("\n## 退步\n\n")
		for _, id := range c.Regressions {
			b.WriteString("- " + id + "\n")
		}
	}
	if len(c.Missing) > 0 {
		b.WriteString("\n## 用例消失\n\n")
		for _, id := range c.Missing {
			b.WriteString("- " + id + "\n")
		}
	}
	if len(c.Improvements) > 0 {
		b.WriteString("\n## 改善\n\n")
		for _, id := range c.Improvements {
			b.WriteString("- " + id + "\n")
		}
	}
	if len(c.Added) > 0 {
		b.WriteString("\n## 新增\n\n")
		for _, id := range c.Added {
			b.WriteString("- " + id + "\n")
		}
	}
	return b.String()
}
