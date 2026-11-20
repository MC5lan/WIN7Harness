// Command evalrun 跑一遍评测基线并落盘报告（Markdown + JSON）。
// 不依赖界面，Linux/Windows 都能跑，用于每次发版前量化“这版到底行不行”。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/eval"
)

func main() {
	outDir := flag.String("out", "eval-out", "报告输出目录")
	tmp := flag.String("tmp", "", "用例临时工作目录（默认在 out 目录下）")
	modelReady := flag.Bool("model", false, "是否具备模型配置（决定端到端用例跑还是跳过）")
	jsonOnly := flag.Bool("json", false, "只输出 JSON 到 stdout，不写文件")
	baseline := flag.String("baseline", "", "基线报告 eval-report.json；提供后逐用例对比并卡退步")
	flag.Parse()

	root := *tmp
	if root == "" {
		root = filepath.Join(os.TempDir(), "harness-eval")
	}
	// 只允许删除专用沙箱目录：它必须位于系统临时目录下、且叶子名以
	// harness-eval 开头。任何其它路径一律拒绝——此前 RemoveAll 是对
	// 用户传入的任意路径无条件递归删除。
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "无效 -tmp 路径：", err)
		os.Exit(2)
	}
	tmpAbs, err := filepath.Abs(os.TempDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法确定临时目录：", err)
		os.Exit(2)
	}
	insideTmp := strings.HasPrefix(strings.ToLower(abs), strings.ToLower(tmpAbs)+string(os.PathSeparator))
	if !insideTmp || !strings.HasPrefix(strings.ToLower(filepath.Base(abs)), "harness-eval") {
		fmt.Fprintf(os.Stderr, "拒绝清理非沙箱目录：%s（-tmp 必须位于临时目录下且以 harness-eval 开头，例如 -tmp %%TEMP%%\\harness-eval-ci）\n", abs)
		os.Exit(2)
	}
	_ = os.RemoveAll(abs)
	rep := eval.Run(root, *modelReady)

	var cmp *eval.Comparison
	if *baseline != "" {
		base, err := eval.LoadReport(*baseline)
		if err != nil {
			fmt.Fprintln(os.Stderr, "读取基线失败：", err)
			os.Exit(2)
		}
		c := eval.Compare(base, rep)
		cmp = &c
	}

	if *jsonOnly {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
		if cmp != nil && !cmp.OK() {
			fmt.Fprintln(os.Stderr, cmp.Markdown())
			os.Exit(2)
		}
		return
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "创建输出目录失败：", err)
		os.Exit(1)
	}
	md := filepath.Join(*outDir, "eval-report.md")
	js := filepath.Join(*outDir, "eval-report.json")
	if err := core.WriteFileAtomic(md, []byte(rep.Markdown()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "写报告失败：", err)
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	if err := core.WriteFileAtomic(js, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "写 JSON 失败：", err)
		os.Exit(1)
	}
	fmt.Printf("评测完成：通过 %d / 失败 %d / 跳过 %d，通过率 %.1f%%，耗时 %.2fs\n",
		rep.Pass, rep.Fail, rep.Skip, rep.Score()*100, float64(rep.TotalMs)/1000)
	fmt.Println("报告：", md)
	fmt.Println("数据：", js)
	if cmp != nil {
		cm := filepath.Join(*outDir, "eval-compare.md")
		cj := filepath.Join(*outDir, "eval-compare.json")
		if err := core.WriteFileAtomic(cm, []byte(cmp.Markdown()), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "写对比报告失败：", err)
			os.Exit(1)
		}
		cb, _ := json.MarshalIndent(cmp, "", "  ")
		if err := core.WriteFileAtomic(cj, cb, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "写对比 JSON 失败：", err)
			os.Exit(1)
		}
		fmt.Printf("基线对比：%s（退步 %d / 消失 %d）\n", cmpStatus(*cmp), len(cmp.Regressions), len(cmp.Missing))
		fmt.Println("对比报告：", cm)
	}
	if rep.Fail > 0 || (cmp != nil && !cmp.OK()) {
		os.Exit(2)
	}
}

func cmpStatus(c eval.Comparison) string {
	if c.OK() {
		return "无退步"
	}
	return "有退步"
}
