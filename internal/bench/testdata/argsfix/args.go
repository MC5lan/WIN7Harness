package argsfix

import "strings"

// Options 是命令行解析结果。
type Options struct {
	Prompt  string
	Timeout int
}

// Parse 解析 -run 与 --timeout。
func Parse(args []string) Options {
	var o Options
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-run":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				o.Prompt = args[i+1]
				i++
			}
		case a == "--timeout":
			if i+1 < len(args) {
				o.Timeout = parseSeconds(args[i+1])
				i++
			}
		}
	}
	return o
}

func parseSeconds(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
