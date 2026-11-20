package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// 本地 OCR：调用 tesseract（若已安装）。
//
// 为什么不是内置 OCR 引擎：Win7 没有可用的系统 OCR API，引入纯 Go 的
// 识别库会让二进制膨胀几十 MB 且中文精度不如 tesseract。因此做成
// "有就用、没有就提示"——没装时 read_image 依然能让视觉模型直接看图，
// 功能不会因为缺依赖而不可用。

// findTesseract 依次尝试 PATH、常见安装目录。
func findTesseract() string {
	if p, err := exec.LookPath("tesseract"); err == nil {
		return p
	}
	candidates := []string{
		`C:\Program Files\Tesseract-OCR\tesseract.exe`,
		`C:\Program Files (x86)\Tesseract-OCR\tesseract.exe`,
		`C:\Tesseract-OCR\tesseract.exe`,
	}
	if runtime.GOOS != "windows" {
		candidates = []string{"/usr/bin/tesseract", "/usr/local/bin/tesseract", "/opt/homebrew/bin/tesseract"}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// OCREngineName 返回引擎名与路径，供状态展示。
func OCREngineName() (string, string) {
	if p := findTesseract(); p != "" {
		return "tesseract", p
	}
	return "", ""
}

// ocrImage 对图片做文字识别。返回识别文本；识别不到文字时返回空串。
func ocrImage(path, lang string, timeout time.Duration) (string, error) {
	bin := findTesseract()
	if bin == "" {
		return "", fmt.Errorf("未安装 OCR 引擎（tesseract）。可安装后重试：Windows 下载 tesseract-ocr 安装包并把安装目录加入 PATH；" +
			"不装也能用——read_image 会把图片直接交给视觉模型阅读")
	}
	if strings.TrimSpace(lang) == "" {
		lang = defaultOCRLang()
	} else {
		// lang 只允许字母数字、+、-、_（语言组合如 chi_sim+eng），
		// 拒绝一切会破坏参数边界的东西——此前直传 tesseract，虽不构成 shell 注入，
		// 但能让 tesseract 以错误的提示输出意料之外的诊断信息。
		ok := true
		for _, r := range lang {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			case r == '+', r == '-', r == '_':
			default:
				ok = false
			}
			if !ok {
				break
			}
		}
		if !ok {
			return "", fmt.Errorf("OCR 语言参数不合法：%q（只允许字母数字与 + - _，例如 chi_sim+eng）", lang)
		}
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// stdout 模式：识别结果直接走标准输出，不在磁盘留临时文件（工作目录可能是只读的）。
	cmd := exec.CommandContext(ctx, bin, path, "stdout", "-l", lang, "--psm", "6")
	// TESSDATA_PREFIX：用户已配置就尊重用户的；否则按安装目录推导
	if v := strings.TrimSpace(os.Getenv("TESSDATA_PREFIX")); v != "" {
		cmd.Env = append(os.Environ(), "TESSDATA_PREFIX="+v)
	} else {
		cmd.Env = append(os.Environ(), "TESSDATA_PREFIX="+filepath.Join(filepath.Dir(bin), "tessdata"))
	}
	// GUI 子系统跑控制台程序会闪一个黑框，隐藏之
	hideCmdWindow(cmd)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("OCR 超时（%s），图片可能过大", timeout)
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("OCR 失败：%s", strings.TrimSpace(firstLineOf(string(ee.Stderr))))
		}
		return "", fmt.Errorf("OCR 失败：%v（若提示找不到语言包，请先执行 tesseract --list-langs 查看已装语言）", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// defaultOCRLang 中文环境优先中英混合，其它环境用英文。
func defaultOCRLang() string {
	if v := strings.TrimSpace(os.Getenv("HARNESS_OCR_LANG")); v != "" {
		return v
	}
	return "chi_sim+eng"
}

func firstLineOf(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i > 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
