//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/eval"
)

// runEval 执行 -eval：跑评测基线，产出可对比的报告。
//
// 退出码语义与自检一致：0 全通过，1 有失败，2 未能开始。
// 与 -selftest 的分工：自检看"这台机器还能不能用"，评测看"这版工具链有没有退化"，
// 所以这里不探测模型、只压工具与索引，适合每个版本发版前后各跑一次对比通过率。
func runEval(opt core.RunOptions) int {
	outDir := opt.SelfTestOut
	if outDir == "" {
		outDir = filepath.Join(core.LocalDataDir(), "eval")
	}
	root := filepath.Join(os.TempDir(), fmt.Sprintf("wh-eval-%d", time.Now().UnixNano()))
	rep := eval.Run(root, false)
	defer os.RemoveAll(root)

	if opt.SelfTestJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "评测结果序列化失败：%v\n", err)
			return 2
		}
		fmt.Println(string(b))
	} else {
		fmt.Print(rep.Markdown())
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "\n报告目录创建失败：%v\n", err)
		return 2
	}
	md := filepath.Join(outDir, "eval-report.md")
	js := filepath.Join(outDir, "eval-report.json")
	if err := core.WriteFileAtomic(md, []byte(rep.Markdown()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "报告写入失败：%v\n", err)
		return 2
	}
	// v1.29：JSON 报告失败不再静默——过去无论成没成功都打印 js 路径，用户去找是空的。
	jsOK := false
	if b, err := json.MarshalIndent(rep, "", "  "); err != nil {
		fmt.Fprintf(os.Stderr, "JSON 序列化失败：%v\n", err)
	} else if err := core.WriteFileAtomic(js, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "JSON 报告写入失败：%v\n", err)
	} else {
		jsOK = true
	}
	fmt.Printf("\n通过 %d / 失败 %d / 跳过 %d，通过率 %.1f%%，总耗时 %dms\n",
		rep.Pass, rep.Fail, rep.Skip, rep.Score()*100, rep.TotalMs)
	if jsOK {
		fmt.Printf("报告已保存：\n  %s\n  %s\n", md, js)
	} else {
		fmt.Printf("报告已保存：\n  %s\n（JSON 版未生成）\n", md)
	}

	if rep.Fail > 0 {
		return 1
	}
	return 0
}
