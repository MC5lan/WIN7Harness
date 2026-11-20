// Package eval 评测基线：用例定义在 eval.go，入口与版本号在此。
package eval

import "win7harness/internal/core"

// Version 当前评测基线版本，随用例集变化递增。
const Version = "1.0"

// CurrentVersion 返回程序版本，报告里展示。
func CurrentVersion() string { return core.Version }
