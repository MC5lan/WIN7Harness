package core

// 更新源常量与架构判断必须在所有平台可见：config.go 的 DefaultConfig 会引用它们，
// 而 CI 在 Linux 上也会编译 internal/core。把这些常量放在 windows 专属的
// update_github.go 里会导致 Linux 编译失败（undefined: UpdateSourceGitHub）。

const (
	UpdateSourceGitHub   = "github"
	UpdateSourceManifest = "manifest"
	DefaultUpdateRepo    = "MC5lan/WIN7Harness"
)

// RuntimeArch 返回 386 / amd64，与 Release 资产命名一致。
func RuntimeArch() string {
	if Is386() {
		return "386"
	}
	return "amd64"
}
