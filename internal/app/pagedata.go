package app

import (
	"os"
	"runtime/metrics"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/coreutil"
	"win7harness/internal/llm"
	"win7harness/internal/remote"
	"win7harness/internal/skills"
)

// pageData 是"需要读磁盘/系统状态"的页面数据快照。
//
// 这些数据过去是在绘制函数里顺手取的：技能清单、子代理定义、项目记忆、
// 崩溃报告、本机网卡、git 身份……每个都要 ReadDir / 读文件 / 起进程。
// 鼠标一动、滚轮一滚，一帧可能触发几十次，Windows 上叠加杀软扫描就是
// 肉眼可见的卡顿，甚至整页"未响应"。
//
// 现在由后台 goroutine 定时刷新，绘制线程只读一份不可变快照：
//   - 界面永不因为页面数据做磁盘 I/O；
//   - 数据最多迟 1 秒（主动增删时 uiDataDrop 立即唤醒刷新），观感仍是实时；
//   - 读快照只持极短读锁，不阻塞绘制。
type pageData struct {
	at time.Time

	skills     []skills.Skill
	agents     []core.AgentDef
	agentsWarn string
	memory     []core.MemEntry
	crashes    []string
	crashMeta  map[string]crashMeta
	ips        []string
	// git 身份：设置页要显示"现在到底是谁在提交"。git config 是子进程，
	// 绝不能每帧跑；这里 30 秒刷新一次足够。
	gitAt    time.Time
	gitName  string
	gitEmail string
	gitTemp  bool
	// 系统代理描述：读注册表/环境变量也要在后台做，避免设置页滚轮踩到
	// WinINET 的慢路径。
	sysProxy string

	// 进程内存不再用 runtime.ReadMemStats：它会停止世界（STW），
	// 在绘制线程里调用等于每 500ms 卡一次界面，实测直接把窗口卡死。
	// runtime/metrics 读的是快照，不停世界。
	heapMB, sysMB, goroutines, gcCycles int
	metrics                             coreutil.MetricsSnapshot
}

// crashMeta 是崩溃报告的大小/时间，后台一次 stat 完供页面直接显示。
type crashMeta struct {
	size int64
	mod  time.Time
}

// pageDataState 是后台刷新器与绘制线程之间的唯一共享点。
type pageDataState struct {
	mu   sync.RWMutex
	snap pageData

	wd atomic.Value // string：当前会话工作目录

	wake chan struct{}

	lastIPs   time.Time
	lastCrash time.Time
	lastGit   time.Time
	lastProxy time.Time
}

const (
	pageDataTick  = 900 * time.Millisecond
	pageDataIPGap = 10 * time.Second
	pageDataCrash = 5 * time.Second
	pageDataGit   = 30 * time.Second
	pageDataProxy = 10 * time.Second
)

func newPageDataState() *pageDataState {
	s := &pageDataState{wake: make(chan struct{}, 1)}
	s.wd.Store("")
	return s
}

// startPageDataLoop 在 v1.29 指标初始化后启动。先同步刷新一次会让启动变慢，
// 所以第一次在 goroutine 里做，页面第一秒显示"加载中"比开窗转圈更可接受。
func (a *App) startPageDataLoop() {
	s := a.pdata
	if s == nil {
		return
	}
	core.SafeGo("app/pagedata.go:loop", func() {
		a.refreshPageData(true)
		tk := time.NewTicker(pageDataTick)
		defer tk.Stop()
		for {
			select {
			case <-tk.C:
				a.refreshPageData(false)
			case <-s.wake:
				a.refreshPageData(true)
			}
		}
	})
}

// setPageDataWD 由 UI 线程在工作区变化时调用；值没变就不唤醒。
func (a *App) setPageDataWD(wd string) {
	s := a.pdata
	if s == nil {
		return
	}
	if old, _ := s.wd.Load().(string); old == wd {
		return
	}
	s.wd.Store(wd)
	s.mu.Lock()
	s.lastGit = time.Time{}
	s.mu.Unlock()
	a.uiDataDrop()
}

// refreshPageData 在后台计算；只有最后 swap 的时候拿写锁。
// 注意：这个函数不得读取 App 的界面字段，只允许读 s.wd 与独立的 store/metrics。
func (a *App) refreshPageData(force bool) {
	s := a.pdata
	if s == nil {
		return
	}
	wd, _ := s.wd.Load().(string)
	now := time.Now()

	// 先取旧快照，保留还没到刷新周期的字段（IP / 崩溃 / git）。
	s.mu.RLock()
	prev := s.snap
	s.mu.RUnlock()

	var next pageData
	next.at = now
	next.skills = prev.skills
	next.agents = prev.agents
	next.agentsWarn = prev.agentsWarn
	next.memory = prev.memory
	next.crashes = prev.crashes
	next.crashMeta = prev.crashMeta
	next.ips = prev.ips
	next.gitAt, next.gitName, next.gitEmail, next.gitTemp = prev.gitAt, prev.gitName, prev.gitEmail, prev.gitTemp
	next.sysProxy = prev.sysProxy
	next.metrics = prev.metrics

	if wd != "" {
		next.skills = skills.Scan(wd)
		next.agents, next.agentsWarn = core.LoadAgentDefs(wd)
		next.memory = core.LoadMemory(wd)
	} else {
		next.skills = nil
		next.agents = nil
		next.memory = nil
	}

	// last* 只由后台刷新 goroutine 读写；真正的 I/O 放到锁外，
	// 避免 UI 读快照时被 git 子进程卡住。
	if force || s.lastCrash.IsZero() || now.Sub(s.lastCrash) >= pageDataCrash {
		next.crashes = core.ListCrashReports(10)
		if len(next.crashes) > 0 {
			meta := make(map[string]crashMeta, len(next.crashes))
			for _, p := range next.crashes {
				if st, err := os.Stat(p); err == nil {
					meta[p] = crashMeta{size: st.Size(), mod: st.ModTime()}
				}
			}
			next.crashMeta = meta
		}
		s.lastCrash = now
	}
	if force || s.lastIPs.IsZero() || now.Sub(s.lastIPs) >= pageDataIPGap {
		if ips := remote.LocalIPs(); len(ips) > 0 {
			next.ips = ips
		}
		s.lastIPs = now
	}
	if force || s.lastGit.IsZero() || now.Sub(s.lastGit) >= pageDataGit {
		if wd != "" {
			next.gitName, next.gitEmail, next.gitTemp = core.GitIdentityFor(wd)
		}
		next.gitAt = now
		s.lastGit = now
	}
	if force || s.lastProxy.IsZero() || now.Sub(s.lastProxy) >= pageDataProxy {
		if env := firstProxyEnv(); env != "" {
			next.sysProxy = "环境变量代理：" + env
		} else {
			sp := llm.ReadSystemProxy()
			if sp.Enabled {
				txt := "系统代理：" + sp.Server
				if strings.TrimSpace(sp.Override) != "" {
					txt += "（含绕过列表）"
				}
				next.sysProxy = txt
			} else {
				next.sysProxy = "系统代理：未启用"
			}
		}
		s.lastProxy = now
	}

	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/sched/goroutines:goroutines"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	metrics.Read(samples)
	if samples[0].Value.Kind() == metrics.KindUint64 {
		next.heapMB = int(samples[0].Value.Uint64() >> 20)
	}
	if samples[1].Value.Kind() == metrics.KindUint64 {
		next.sysMB = int(samples[1].Value.Uint64() >> 20)
	}
	if samples[2].Value.Kind() == metrics.KindUint64 {
		next.goroutines = int(samples[2].Value.Uint64())
	}
	if samples[3].Value.Kind() == metrics.KindUint64 {
		next.gcCycles = int(samples[3].Value.Uint64())
	}
	if a.v29 != nil && a.v29.metrics != nil {
		next.metrics = a.v29.metrics.Snapshot()
	}

	s.mu.Lock()
	// 扫描期间切了工作区：这份结果对当前会话已经过期，丢弃等下一轮。
	if cur, _ := s.wd.Load().(string); cur == wd {
		s.snap = next
	}
	s.mu.Unlock()
}

// uiData 返回一份快照副本。只能在绘制线程调用；不会做任何 I/O。
func (a *App) uiData() pageData {
	s := a.pdata
	if s == nil {
		return pageData{}
	}
	// UI 线程顺带把当前工作目录告诉后台刷新器：切会话/切目录后下一次刷新
	// 就会读新目录，不需要在每个切换点手工调用。
	a.setPageDataWD(a.sessionWorkDir())
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// uiDataDrop 让后台立刻刷新（增删了文件/规则/技能之后调用）。
func (a *App) uiDataDrop() {
	s := a.pdata
	if s == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// 下面几个小函数是给页签上的计数用的，走的都是同一份快照。

func (a *App) cachedSkillCount() int { return len(a.uiData().skills) }

func (a *App) cachedAgentCount() int { return len(a.uiData().agents) }

func (a *App) cachedMemoryCount() int { return len(a.uiData().memory) }

func (a *App) cachedCrashList() []string { return a.uiData().crashes }

// cachedCrashMeta 返回崩溃报告的大小与时间；没有记录时返回零值。
func (a *App) cachedCrashMeta(p string) (int64, time.Time) {
	m := a.uiData().crashMeta
	if v, ok := m[p]; ok {
		return v.size, v.mod
	}
	return 0, time.Time{}
}

func (a *App) cachedLocalIPs() []string { return a.uiData().ips }
