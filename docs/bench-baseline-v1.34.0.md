# WIN7 Harness 工程 bench 基线 v1.34.0

- 模型：deepseek-flash
- 任务：22（5 个原 fixture + 17 个生成 fixture）
- 结果：22/22 通过
- 总耗时：949.1s（15.8 分钟）
- token：约 1.02M
- 备注：stablesort 首轮 fixture 断言方向写错，已修正并复跑（42k token 通过）；下面的原始报告保留了首轮数据。
- 复跑：`go run ./cmd/bench -task <id> -go <go.exe> -out bench-out`

---
# Agent Bench 报告

- 时间：2026-11-19 16:16:38
- 通过：22 / 22
- 总耗时：349.1s

| 任务 | 结果 | 耗时 | token | 说明 |
| --- | --- | --- | --- | --- |
| argsfix | pass | 13.3s | 42720 | 改了什么：在 `args.go` 的 `Parse` 里新增 `strings.HasPrefix(a, "--timeout=")` 分支，把等号后的部分交给 `parseSeconds`，原来的 `--timeout 90` 分支保持不动；`go test ./...` 已从失败变为 `ok`。 |
| slug | pass | 12.5s | 41478 | 改了什么：把 `slug.go` 里那行 `strings.ReplaceAll(..., " ", "-")` 换成了 `strings.Join(strings.Fields(strings.ToLower(s)), "-")`，这样会先转为小写，再用 `Fields` 去掉首尾空白，并把任意长度的连续空白折叠成一… |
| dedupe | pass | 10.9s | 32685 | 已修复：`dedupe.go` 在做 `seen` 判重时，之前会无条件把元素加入 `out`；现在先查 `seen`，重复就跳过，只把首次出现的元素写入结果。验证：`go test ./...` 通过（ok dedupe）。未改动测试文件与 go.mod。 |
| jsonfield | pass | 11.9s | 42065 | 把 `user.go:8` 中 `User.Email` 的标签从 `json:"email_address,omitempty"` 改成 `json:"email,omitempty"`，`go test ./...` 已通过。 |
| multifile | pass | 13.8s | 52685 | `go test ./...` 已通过（client、config 两个包均 ok）。 /  / 改动（仅非测试源文件）： / - `config/config.go`：`Normalize` 里 `Retries` 的判断由 `< 0 → 0` 改为 `<= 0 → 1`（`TimeoutSec <= 0 → 30`… |
| runerev | pass | 11.7s | 41104 | 已将 `Reverse` 中的 `[]byte(s)` 改为 `[]rune(s)`，即按 Unicode 字符反转，不再按字节反转；`go test ./...` 现在通过。 |
| avgfloat | pass | 11.2s | 32619 | 测试已通过。 /  / 改动：`mean.go:11` 把 `float64(sum / len(xs))`（先做整数除法再转 float64，导致 3/2 得 1）改成 `float64(sum) / float64(len(xs))`，实现真正的浮点平均值。 /  / 验证：`go test ./...` 由 `-… |
| clamp | pass | 12.3s | 40929 | 测试通过。 /  / 改动：在 `clamp.go` 的 `Clamp` 中补上上界判断 `if v > hi { return hi }`，现在函数同时约束下界与上界，`go test ./...` 由 FAIL 变为 ok。 /  / 风险：若传入 `lo > hi`，现有分支顺序会返回 `lo`（未做参数合法性校… |
| parsebool | pass | 10.6s | 32807 | 我在 `parse.go:10-13` 的真值分支中增加了 `yes`/`on`，在假值分支中增加了 `no`/`off`；由于先用 `strings.ToLower` 配合 `TrimSpace` 做了归一化，所以这些值不区分大小写。`go test ./...` 已通过，退出码为 0。这次改动只涉及非测试源文件，没… |
| mapmerge | pass | 12.3s | 41599 | 已改 config.go 里的 `Merge`：它不再直接修改 `base.Values`，而是先新建一个 map，把 base 中的键值复制进去，再用 override 中的同名键覆盖，最后返回包含这个新 map 的 `Config`。`go test ./...` 现在通过。 |
| svcmiss | pass | 13.3s | 42246 | `go test ./...` 已通过，退出码 0，`svcmiss/service` 为 ok。 /  / 改了什么：`service/service.go` 里的 `Lookup` 不再忽略 `Store.Get` 的 `ok` 返回值；当 id 不存在时，返回 `fmt.Errorf("service: id %… |
| http404 | pass | 11.0s | 33572 | 失败原因：Handler 直接执行 `db[id]`，查不到时得到空字符串，仍返回 200 和 `{"name":""}`。 /  / 改法：把查找改成带 ok 判断的 `name, ok := db[id]`，当 `!ok` 时调用 `http.NotFound(w, r)`（返回 404）并提前退出；只有查到用户时… |
| paging | pass | 11.3s | 33048 | 改了什么：把 `page.go` 里的返回值从 `page * size` 改为 `(page - 1) * size`，使页码按 1-based 计算，例如第 1 页偏移 0、第 3 页偏移 20；`go test ./...` 已通过。 |
| retrymax | pass | 12.1s | 40848 | 已修复 retry.go 中 `MaxAttempts` 的错误返回值（原 `return n - 1` 改为 `return n`），`n <= 0` 分支保持不变；`go test ./...` 通过。 |
| ttlcache | pass | 12.1s | 32971 | `cache.go` 的 `Get` 增加了过期判断 `if !now.Before(e.Expires) { return "", false }`，`go test ./...` 已通过（覆盖 TestExpiredMiss）。风险：过期条目仍留在 map 里，只是不返回，未做清理。 |
| errwrap | pass | 11.7s | 40716 | 改动：`errs.go:11` 把 `fmt.Errorf("load: %v", ErrNotFound)` 改为 `"load: %w"`，使包装后的错误链保留原始错误；`go test ./...` 现已通过（ok errwrap）。 /  / 风险：`fmt.Errorf` 加 `%w` 需要 Go 1.13+… |
| pathjoin | pass | 15.0s | 41330 | `go test ./...` 已通过。 /  / 改动：把 `path.go` 里的 `Join` 从 `strings.Join(parts, "/")` 改为直接委托 `filepath.Join(parts...)`，从而使用平台分隔符（Windows 下为 `\`）并自动清理多余分隔符；未触碰任何 `*_te… |
| stablesort | pass | 75.1s | 167550 | 已修复，`go test ./...` 通过，`go vet` 也没有问题。只改了 `sort.go`（go.mod、sort_test.go 大小未变，未触碰）。 /  / 一句话说明：把 `sort.go:11` 的 `sort.Slice` 换成 `sort.SliceStable`，使 Key 相同的元素保持原… |
| cancel | pass | 13.4s | 42323 | `go test ./...` 已通过（ok cancel）。 /  / 改动：只在 `worker.go` 的循环里加了一处判断——每轮调用 `fn` 前先检查 `ctx.Err()`，一旦上下文已取消就立即 `return`，因此 `fn` 不会再被调用（测试中 `seen` 停在 2）。未触碰任何 `*_test… |
| csvparse | pass | 14.8s | 41805 | `go test ./...` 已通过（`go vet` 无告警）。 /  / 改动：在 `parse.go` 中把原来的 `strings.Split` 换成手写的状态机解析。解析器遇到引号就切换到引号模式；在引号内，逗号会被当作普通字符，`""` 表示一个转义的字面引号，字段末尾的引号会切回正常模式；只有在引号外遇… |
| longhorizon | pass | 19.1s | 48752 | `go test ./...` 全部通过（10 个包全 ok）。 /  / 改动：只把 p01~p10 各自 `pNN.go` 里 `Value()` 的返回值从 `0` 改为对应编号（p01→1 … p10→10），未触碰任何 `*_test.go`、`go.mod`、`go.sum`。 |
| refactor | pass | 19.7s | 57841 | `go test ./...` 通过（`ok refactor/calc`），`go vet` 无告警。 /  / 改动内容：把 calc.go 里的函数 `Old` 重命名为 `Compute`，并把 use01~use10.go 中对应的 10 处调用点同步改成 `Compute`；没有修改测试文件、go.mod … |
