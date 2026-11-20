package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/coreutil"
	"win7harness/internal/remote"
)

// v129State 存放 v1.29 的四件新东西：
//   - queue   任务队列（忙时不再丢弃定时任务）
//   - metrics 运行指标（/perf 与手机端 /api/metrics）
//   - devices 设备表（token 落盘、可吊销）
//   - turnAt  本轮开始时间，用于指标埋点
type v129State struct {
	queue   *core.TaskQueue
	metrics *coreutil.Metrics
	devices *core.DeviceStore
	turnAt  time.Time
	lastGC  time.Time
}

// initV129 在 initV128 之后调用。
func (a *App) initV129() {
	// 任务队列挂到 KV：排队的任务在重启后不再丢失（旧版仅内存，崩溃即蒸发）。
	// KV 打不开时退化为内存队列，不影响启动——持久化是增强，不是 correctness 依赖。
	kv, kvErr := core.DataKV()
	if kvErr != nil {
		core.Logf("任务队列持久化不可用，退化为内存队列：%v", kvErr)
	}
	st := &v129State{
		queue:   core.NewTaskQueue(64, kv),
		metrics: coreutil.NewMetrics(2048),
	}
	a.v29 = st

	// 设备表：坏了要说话，不能让用户以为"又要我扫码"。
	st.devices = core.LoadDevices(core.DevicesPath())
	if st.devices.LoadErr != "" {
		a.sysNote(st.devices.LoadErr)
	}
	// 启动时不再自动清过期设备：那是静默删用户数据，而且删了之后
	// 用户在"设备"页里根本看不到自己有过哪些设备。
	// 过期 token 本来就会被 Verify 拒掉，清理交给页面上的「清理已过期」按钮。

	// 自更新善后：删掉上一版留下的 .old；读取上次替换失败的原因。
	if exe, err := core.SelfExePath(); err == nil {
		if n := core.CleanupOldExe(filepath.Dir(exe)); n > 0 {
			core.Logf("自更新：已清理上一版残留 %d 个 .old 文件", n)
		}
	}
	if msg := core.PendingUpdate(core.UpdatesDir()); msg != "" {
		a.sysNote("上次自更新没有成功：" + msg + "\r\n当前仍在用旧版本，可重试 /update apply。")
	}

	// 配置损坏兜底：备份过就明确告诉用户，别让人以为设置被吞了。
	if msg := core.TakeConfigLoadError(); msg != "" {
		a.sysNote(msg)
	}

	a.hookRemoteV129()
}

// hookRemoteV129 把手机端要用的能力挂上去。回调都在这里收口，
// remote 包本身不认识 App，方便单独测。
func (a *App) hookRemoteV129() {
	st := a.v29
	if st == nil || a.v27 == nil || a.v27.rctl == nil {
		return
	}
	r := a.v27.rctl

	r.SetStopHandler(func() (string, bool) { return a.stopFromPhone() })

	r.SetPlansHook(
		func() []remote.PlanView {
			var out []remote.PlanView
			a.v27.schedMu.Lock()
			defer a.v27.schedMu.Unlock()
			if a.v27.sched == nil {
				return out
			}
			for _, s := range a.v27.sched.Items {
				if s == nil {
					continue
				}
				v := remote.PlanView{
					ID:   s.ID,
					Name: clipRunes(s.Prompt, 30),
					Spec: s.Describe(),
					On:   s.Enabled,
					Runs: s.Runs,
				}
				if !s.NextRun.IsZero() {
					v.Next = s.NextRun.Format("01-02 15:04")
				}
				if !s.LastRun.IsZero() {
					v.Last = s.LastRun.Format("01-02 15:04")
				}
				out = append(out, v)
			}
			return out
		},
		func(id string, on bool) (bool, string) {
			a.v27.schedMu.Lock()
			defer a.v27.schedMu.Unlock()
			if a.v27.sched == nil {
				return false, "本机还没有定时任务清单"
			}
			// SetEnabled 内部会重算下次时间：重新启用不该立刻补跑一串。
			it, ok := a.v27.sched.SetEnabled(id, on, time.Now())
			if !ok {
				return false, "没找到这条计划"
			}
			if err := a.v27.sched.Save(); err != nil {
				return false, "保存失败：" + err.Error()
			}
			state := "暂停"
			if on {
				state = "启用"
			}
			return true, fmt.Sprintf("%s 已%s：%s", state, state, clipRunes(it.Prompt, 24))
		},
	)

	r.SetDevicesHook(
		func(callerHash string) []remote.DeviceView {
			var out []remote.DeviceView
			for _, d := range st.devices.List() {
				v := remote.DeviceView{ID: d.ID, Name: d.Name}
				if !d.LastSeen.IsZero() {
					v.Seen = d.LastSeen.Format("01-02 15:04")
				}
				if !d.ExpiresAt.IsZero() {
					v.Expires = d.ExpiresAt.Format("01-02 15:04")
				}
				// Current = 该设备登记时的哈希 == 本次请求的 token 哈希
				v.Current = callerHash != "" && d.TokenHash == callerHash
				out = append(out, v)
			}
			return out
		},
		func(key string) (string, bool) {
			d, ok := st.devices.Revoke(key)
			if !ok {
				return "", false
			}
			// 吊销不仅删落盘表：内存里的 token 必须立即作废，
			// 否则被踢的手机在进程存活期内仍能发指令、批准高危确认。
			if a.v27 != nil && a.v27.rctl != nil && d.Tok != "" {
				a.v27.rctl.ForgetToken(d.Tok)
			}
			return d.Name, true
		},
	)

	r.SetDeviceRegistry(
		func(name, tok string) {
			if _, err := st.devices.Add(name, tok, remote.TokenTTL); err != nil {
				core.Logf("设备登记失败：%v", err)
			}
		},
		func(tok string) bool {
			_, ok := st.devices.Verify(tok, time.Now())
			return ok
		},
	)

	r.SetMetricsHook(func() interface{} { return st.metrics.Snapshot() })
}

// stopFromPhone 手机点"停止"：停当前生成 + 清空待执行队列。
func (a *App) stopFromPhone() (string, bool) {
	stopped := false
	parts := []string{}
	if a.eng != nil && a.eng.Running() {
		a.eng.Stop()
		stopped = true
		parts = append(parts, "当前生成")
	}
	if a.v29 != nil {
		if n := a.v29.queue.Clear(); n > 0 {
			stopped = true
			parts = append(parts, fmt.Sprintf("队列中 %d 个任务", n))
		}
	}
	if !stopped {
		return "当前没有正在跑的任务", false
	}
	msg := "已停止 " + strings.Join(parts, " 与 ")
	a.sysNote(msg)
	return msg, true
}

// tickV129 由主循环调用：消费队列里的活，定期清过期设备。
func (a *App) tickV129(now time.Time) {
	st := a.v29
	if st == nil {
		return
	}
	a.drainQueue(now)
	if now.Sub(st.lastGC) > 10*time.Minute {
		st.lastGC = now
		// 只统计、不删除：过期设备留在列表里让用户自己看见再清。
		// 静默删掉的结果是「我的设备怎么没了」，而它本来只是个过期 token——
		// Verify 已经拒掉它，没有任何安全理由要偷偷清理。
		expired := 0
		for _, d := range st.devices.List() {
			if d.Expired(now) {
				expired++
			}
		}
		if expired > 0 {
			core.Logf("设备表：%d 台设备已过期（可在「扩展 → 设备」页清理）", expired)
		}
	}
}

// drainQueue 空闲时取出一个排队任务执行。忙就等下一帧，
// 队列本身有上限，不会把老机内存吃穿。
func (a *App) drainQueue(now time.Time) {
	st := a.v29
	if st == nil || a.streaming || a.eng == nil {
		return
	}
	it, ok := st.queue.Pop()
	if !ok {
		return
	}
	// 先释放去重键再执行：执行失败要能重新排队，而不是被自己的键挡住。
	st.queue.Done(it.Dedup)
	if !a.sendText(it.Prompt) {
		st.queue.Push(it)
		return
	}
	st.turnAt = now
	if it.Source != "" {
		a.toast("开始执行排队任务："+clipRunes(it.Prompt, 24), false)
	}
}

// enqueueTask 把因忙碌被推迟的任务排进队列。
func (a *App) enqueueTask(prompt, dedup, origin string) bool {
	st := a.v29
	if st == nil {
		return false
	}
	r := st.queue.Push(core.TaskItem{Prompt: prompt, Dedup: dedup, Source: origin})
	if !r.OK {
		if r.Reason != "" && r.Reason != "重复入队" {
			a.sysNote("任务排队失败：" + r.Reason)
		}
		core.Logf("任务排队被拒：%s（%s）", r.Reason, clipRunes(prompt, 30))
		return false
	}
	return true
}

// withMetricsExtra 把运行指标塞进诊断包：老机"卡"的时候，
// 光看日志看不出是模型慢还是工具慢，指标能一句话说清。
func (a *App) withMetricsExtra(opt core.DiagOptions) core.DiagOptions {
	if a.v29 == nil {
		return opt
	}
	if opt.Extra == nil {
		opt.Extra = map[string]string{}
	}
	b := &strings.Builder{}
	b.WriteString(coreutil.MetricsText(a.v29.metrics.Snapshot()))
	b.WriteString("\r\n\r\n队列：")
	b.WriteString(fmt.Sprintf("待执行 %d 个\r\n", a.v29.queue.Len()))
	opt.Extra["metrics.txt"] = b.String()
	return opt
}

// observeTurn 记录一轮耗时，供 /perf 与手机端指标使用。
func (a *App) observeTurn(ok bool) {
	st := a.v29
	if st == nil || st.turnAt.IsZero() {
		return
	}
	st.metrics.Observe("turn", time.Since(st.turnAt), ok)
	st.turnAt = time.Time{}
}

// observeTool 记录单次工具耗时。
func (a *App) observeTool(name string, d time.Duration, ok bool) {
	if a.v29 == nil || name == "" {
		return
	}
	a.v29.metrics.Observe("tool."+name, d, ok)
}
