// depguard：依赖准入审查（CI / 提交前自检）。
//
// 为什么需要：本项目锁 Go 1.20（最后一个支持 Win7 的版本），产物是给老机器
// 用的单文件 exe。新增依赖若要求更高 Go 版本、或带强 copyleft 许可证，
// 往往到发布环节才炸。这里在提交前把它们挡住。
//
// 阻断项（exit 1）：
//  1. 依赖 go.mod 声明的 Go 版本 > 1.20（用 1.20 编不过）
//  2. 强 copyleft 许可证（GPL / AGPL / SSPL / Commons Clause）——分发合规风险
//
// 警告项（不阻断）：许可证未识别 / 未找到、直接依赖数量。
//
// 用法：
//
//	go run ./cmd/depguard          # 审查
//	go run ./cmd/depguard -json    # 机器可读输出
//	GO=/path/to/go.exe go run ...  # 指定 go（与 build.sh 的 GO 变量一致）
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type module struct {
	Path      string `json:"Path"`
	Version   string `json:"Version"`
	Dir       string `json:"Dir"`
	GoVersion string `json:"GoVersion"`
	Indirect  bool   `json:"Indirect"`
	Main      bool   `json:"Main"`
}

const maxGoMajor, maxGoMinor = 1, 20

type licenseRule struct {
	pat  *regexp.Regexp
	name string
}

var (
	blockLicenses = []licenseRule{
		{regexp.MustCompile(`(?i)GNU AFFERO GENERAL PUBLIC LICENSE|\bAGPL\b`), "AGPL"},
		{regexp.MustCompile(`(?i)GNU GENERAL PUBLIC LICENSE|\bGPL-?[23]\b`), "GPL"},
		{regexp.MustCompile(`(?i)SERVER SIDE PUBLIC LICENSE|\bSSPL\b`), "SSPL"},
		{regexp.MustCompile(`(?i)COMMONS CLAUSE`), "Commons Clause"},
	}
	knownLicenses = []licenseRule{
		{regexp.MustCompile(`(?i)APACHE LICENSE`), "Apache-2.0"},
		{regexp.MustCompile(`(?i)MIT LICENSE|PERMISSION IS HEREBY GRANTED, FREE OF CHARGE`), "MIT"},
		{regexp.MustCompile(`(?i)REDISTRIBUTION AND USE IN SOURCE AND BINARY FORMS`), "Go-BSD"},
		{regexp.MustCompile(`(?i)BSD`), "BSD"},
		{regexp.MustCompile(`(?i)MOZILLA PUBLIC LICENSE`), "MPL-2.0"},
		{regexp.MustCompile(`(?i)\bISC LICENSE\b`), "ISC"},
		{regexp.MustCompile(`(?i)UNLICENSE|\bCC0\b`), "Public domain"},
	}
	goVerRe = regexp.MustCompile(`^(\d+)\.(\d+)`)
)

// projectRoot 从当前目录向上找 go.mod，作为 go list 的工作目录。
func projectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("向上找不到 go.mod")
		}
		dir = parent
	}
}

func parseGoVersion(s string) (major, minor int, ok bool) {
	m := goVerRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

func licenseOf(dir string) (name, fileName string) {
	if dir == "" {
		return "", ""
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	for _, e := range ents {
		low := strings.ToLower(e.Name())
		if !strings.HasPrefix(low, "license") && !strings.HasPrefix(low, "copying") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if len(b) > 20000 {
			b = b[:20000]
		}
		text := string(b)
		up := strings.ToUpper(text)
		// 双许可证优先：如 freetype 的 "choice of ... BSD-like ... or GPL"，
		// 可选非 copyleft 许可，不该被判成 GPL 阻断。
		if strings.Contains(up, "YOUR CHOICE OF") &&
			(strings.Contains(up, "BSD") || strings.Contains(up, "FREETYPE LICENSE")) {
			return "dual(BSD/GPL)", e.Name()
		}
		// LGPL 优先于 GPL：LGPL 正文里也含 "GNU General Public License" 字样，
		// 但它允许链接到专有程序，属警告级而非强 copyleft 阻断。
		if strings.Contains(up, "GNU LESSER GENERAL PUBLIC LICENSE") {
			return "LGPL", e.Name()
		}
		for _, r := range blockLicenses {
			if r.pat.MatchString(text) {
				return "BLOCK:" + r.name, e.Name()
			}
		}
		for _, r := range knownLicenses {
			if r.pat.MatchString(text) {
				return r.name, e.Name()
			}
		}
		return "unknown", e.Name()
	}
	return "", ""
}

type report struct {
	Modules  int            `json:"modules"`
	Direct   int            `json:"direct"`
	Licenses map[string]int `json:"licenses"`
	Blockers []string       `json:"blockers"`
	Warnings []string       `json:"warnings"`
}

func main() {
	asJSON := flag.Bool("json", false, "机器可读输出")
	flag.Parse()

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "依赖审查失败：", err)
		os.Exit(1)
	}
	goBin := os.Getenv("GO")
	if goBin == "" {
		goBin = "go"
	}
	cmd := exec.Command(goBin, "list", "-m", "-json", "all")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "依赖审查失败：go list 出错：%v\n", err)
		os.Exit(1)
	}

	r := report{Licenses: map[string]int{}}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var m module
		if err := dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "依赖审查失败：解析 go list 输出：%v\n", err)
			os.Exit(1)
		}
		if m.Path == "" || m.Main {
			continue
		}
		// go list -m all 会列出 go.sum 里的全部历史模块，未下载的（Dir 为空）
		// 并非实际依赖——它们的许可证信息也无从读取，跳过以免制造 58 条噪音。
		if m.Dir == "" {
			continue
		}
		r.Modules++
		if !m.Indirect {
			r.Direct++
		}

		// 1. Go 版本要求
		if maj, min, ok := parseGoVersion(m.GoVersion); ok && (maj > maxGoMajor || (maj == maxGoMajor && min > maxGoMinor)) {
			r.Blockers = append(r.Blockers, fmt.Sprintf("%s 要求 Go %d.%d（项目锁 1.20，Win7 兼容红线）", m.Path, maj, min))
		}

		// 2. 许可证
		lic, file := licenseOf(m.Dir)
		if lic == "" {
			lic = "unknown"
		}
		r.Licenses[lic]++
		if strings.HasPrefix(lic, "BLOCK:") {
			r.Blockers = append(r.Blockers, fmt.Sprintf("%s 使用 %s 许可证（强 copyleft，分发合规风险）", m.Path, strings.TrimPrefix(lic, "BLOCK:")))
		} else if lic == "unknown" {
			if file != "" {
				r.Warnings = append(r.Warnings, fmt.Sprintf("%s 许可证未识别（找到 %s）", m.Path, file))
			} else {
				r.Warnings = append(r.Warnings, fmt.Sprintf("%s 未找到许可证文件", m.Path))
			}
		}
	}

	if *asJSON {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("依赖审查：%d 个模块（直接依赖 %d 个）\n", r.Modules, r.Direct)
		keys := make([]string, 0, len(r.Licenses))
		for k := range r.Licenses {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s×%d", k, r.Licenses[k]))
		}
		fmt.Println("许可证分布：" + strings.Join(parts, ", "))
		if len(r.Warnings) > 0 {
			fmt.Println("\n警告（人工核对）：")
			for _, w := range r.Warnings {
				fmt.Println("  " + w)
			}
		}
		if len(r.Blockers) > 0 {
			fmt.Println("\n阻断项（必须处理）：")
			for _, b := range r.Blockers {
				fmt.Println("  " + b)
			}
			os.Exit(1)
		}
		fmt.Println("依赖准入检查通过")
	}
	if len(r.Blockers) > 0 {
		os.Exit(1)
	}
}
