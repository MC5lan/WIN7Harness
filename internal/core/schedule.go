package core

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// —— 定时任务 ——
//
// 定位：让 harness 在没人看着的时候按点自己跑起来（"每天晚上把当天的改动补上测试"）。
//
// 刻意不做 cron 全语法。Win7 老机器上的用户真正要的只有两种：
//   - 每 N 分钟/小时（every 30m）
//   - 每天固定时刻（daily 09:30）
// 语法越全，写错的机会越多，而报错信息对不写代码的用户毫无意义。
// 表达不了的排期，用"每 30 分钟 + 提示词里写清判断条件"也能达到八成效果。

// Schedule 一条定时任务。
type Schedule struct {
	ID       string    `json:"id"`
	Prompt   string    `json:"prompt"`
	EverySec int       `json:"every_sec,omitempty"` // 间隔触发（秒），与 DailyAt 二选一
	DailyAt  string    `json:"daily_at,omitempty"`  // "HH:MM"，本地时间
	WorkDir  string    `json:"work_dir,omitempty"`  // 触发时的工作目录，空表示用默认工作区
	NextRun  time.Time `json:"next_run"`
	LastRun  time.Time `json:"last_run,omitempty"`
	Runs     int       `json:"runs"`
	Enabled  bool      `json:"enabled"`
	Created  time.Time `json:"created,omitempty"`
}

// MinEverySec 是最小间隔。低于 30 秒的循环对本地模型没有意义，
// 只会把界面线程和 token 一起烧穿，直接在解析阶段挡掉。
const MinEverySec = 30

// ParseScheduleSpec 解析排期表达式。
// 支持：30s / 30m / 2h / 1d / every 30m / daily 09:30 / 每天 09:30（中文别名也认，用户不总记得英文）。
func ParseScheduleSpec(spec, prompt, workDir string, now time.Time) (*Schedule, error) {
	raw := strings.TrimSpace(spec)
	low := strings.ToLower(raw)
	low = strings.TrimSpace(strings.TrimPrefix(low, "every"))
	for _, p := range []string{"每天", "每日"} {
		if strings.HasPrefix(raw, p) {
			raw = "daily " + strings.TrimSpace(strings.TrimPrefix(raw, p))
			low = strings.ToLower(raw)
		}
	}
	if raw == "" {
		return nil, errors.New("缺少排期，例如：/loop 30m 跑一遍测试")
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("缺少要执行的提示词")
	}

	s := &Schedule{Prompt: strings.TrimSpace(prompt), WorkDir: workDir, Enabled: true, Created: now}

	switch {
	case strings.HasPrefix(low, "daily"):
		hhmm := strings.TrimSpace(strings.TrimPrefix(low, "daily"))
		hh, mm, err := parseHHMM(hhmm)
		if err != nil {
			return nil, err
		}
		s.DailyAt = fmt.Sprintf("%02d:%02d", hh, mm)
		s.NextRun = nextDaily(now, hh, mm)
	case strings.HasPrefix(low, "weekly"):
		// 每周固定日：weekly mon 09:00。比 cron 的"1-5"好懂，且够用。
		fields := strings.Fields(low)
		if len(fields) != 3 {
			return nil, errors.New("用法：/loop weekly mon 09:00 <提示词>")
		}
		wd, err := parseWeekday(fields[1])
		if err != nil {
			return nil, err
		}
		hh, mm, err := parseHHMM(fields[2])
		if err != nil {
			return nil, err
		}
		s.DailyAt = fmt.Sprintf("%s %02d:%02d", weekdayName(wd), hh, mm)
		s.NextRun = nextWeekly(now, wd, hh, mm)
	default:
		// 用剥离过 every 前缀的 low：用户写 "every 30m" 时 raw 仍是原文，
		// 直接喂给时长解析会把 "every" 当成数字。
		sec, err := parseDurationSec(low)
		if err != nil {
			return nil, err
		}
		if sec < MinEverySec {
			return nil, fmt.Errorf("间隔不能小于 %d 秒", MinEverySec)
		}
		s.EverySec = sec
		s.NextRun = now.Add(time.Duration(sec) * time.Second)
	}
	return s, nil
}

func parseHHMM(s string) (int, int, error) {
	s = strings.TrimSpace(strings.TrimSuffix(s, "点"))
	s = strings.ReplaceAll(s, "：", ":")
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("时间格式应为 HH:MM，收到 %q", s)
	}
	hh, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	mm, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("时间超出范围：%q", s)
	}
	return hh, mm, nil
}

func parseDurationSec(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("缺少间隔")
	}
	unit := s[len(s)-1]
	numStr := s
	mult := 1
	switch unit {
	case 's':
		mult = 1
		numStr = s[:len(s)-1]
	case 'm':
		mult = 60
		numStr = s[:len(s)-1]
	case 'h':
		mult = 3600
		numStr = s[:len(s)-1]
	case 'd':
		mult = 86400
		numStr = s[:len(s)-1]
	default:
		if v, err := strconv.Atoi(s); err == nil {
			return v * 60, nil // 裸数字按分钟，符合"每 30"的直觉
		}
		return 0, fmt.Errorf("间隔要带单位（s/m/h/d），收到 %q", s)
	}
	v, err := strconv.Atoi(strings.TrimSpace(numStr))
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("间隔数字无效：%q", s)
	}
	// 防 32 位 int 溢出（"50000d" 在 32 位会绕回去）
	limit := int64(math.MaxInt64)
	if strconv.IntSize == 32 {
		limit = int64(math.MaxInt32)
	}
	if int64(v)*int64(mult) > limit {
		return 0, fmt.Errorf("间隔超出%d 位上限：%q", strconv.IntSize, s)
	}
	return v * mult, nil
}

var weekdayAliases = map[string]time.Weekday{
	"mon": time.Monday, "monday": time.Monday, "周一": time.Monday,
	"tue": time.Tuesday, "tuesday": time.Tuesday, "周二": time.Tuesday,
	"wed": time.Wednesday, "wednesday": time.Wednesday, "周三": time.Wednesday,
	"thu": time.Thursday, "thursday": time.Thursday, "周四": time.Thursday,
	"fri": time.Friday, "friday": time.Friday, "周五": time.Friday,
	"sat": time.Saturday, "saturday": time.Saturday, "周六": time.Saturday,
	"sun": time.Sunday, "sunday": time.Sunday, "周日": time.Sunday, "周天": time.Sunday,
}

func parseWeekday(s string) (time.Weekday, error) {
	if wd, ok := weekdayAliases[strings.ToLower(s)]; ok {
		return wd, nil
	}
	return 0, fmt.Errorf("星期无效：%q（用 mon..sun）", s)
}

func weekdayName(wd time.Weekday) string {
	return [...]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}[int(wd)]
}

func nextDaily(from time.Time, hh, mm int) time.Time {
	t := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, from.Location())
	if !t.After(from) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func nextWeekly(from time.Time, wd time.Weekday, hh, mm int) time.Time {
	t := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, from.Location())
	for i := 0; i < 8; i++ {
		if t.Weekday() == wd && t.After(from) {
			return t
		}
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// nextRun 由已解析的排期算下一次触发时刻。
func (s *Schedule) nextRun(from time.Time) time.Time {
	if s.EverySec > 0 {
		return from.Add(time.Duration(s.EverySec) * time.Second)
	}
	if s.DailyAt != "" {
		fields := strings.Fields(s.DailyAt)
		if len(fields) == 2 {
			wd, err1 := parseWeekday(fields[0])
			hh, mm, err2 := parseHHMM(fields[1])
			if err1 == nil && err2 == nil {
				return nextWeekly(from, wd, hh, mm)
			}
		}
		if hh, mm, err := parseHHMM(s.DailyAt); err == nil {
			return nextDaily(from, hh, mm)
		}
	}
	return from.Add(24 * time.Hour)
}

// Describe 给界面用的一行说明。
// NextRunAfter 返回从 from 起算的下次触发时间。
// 界面在"停用后重新启用"时要立刻排下一次，否则会等一个空 tick。
func (s *Schedule) NextRunAfter(from time.Time) time.Time { return s.nextRun(from) }

func (s *Schedule) Describe() string {
	var when string
	switch {
	case s.EverySec > 0:
		when = "每 " + humanEvery(s.EverySec)
	case s.DailyAt != "":
		fields := strings.Fields(s.DailyAt)
		if len(fields) == 2 {
			when = "每" + fields[0] + " " + fields[1]
		} else {
			when = "每天 " + s.DailyAt
		}
	}
	if !s.Enabled {
		when += "（已暂停）"
	}
	next := ""
	if !s.NextRun.IsZero() {
		next = "，下次 " + s.NextRun.Format("01-02 15:04")
	}
	return fmt.Sprintf("%s · %s%s · 已跑 %d 次", s.ID, when, next, s.Runs)
}

func humanEvery(sec int) string {
	switch {
	case sec%86400 == 0:
		return fmt.Sprintf("%d 天", sec/86400)
	case sec%3600 == 0:
		return fmt.Sprintf("%d 小时", sec/3600)
	case sec%60 == 0:
		return fmt.Sprintf("%d 分钟", sec/60)
	default:
		return fmt.Sprintf("%d 秒", sec)
	}
}

// —— 持久化 ——

// SchedulesPath 定时任务清单的落盘位置。
func SchedulesPath() string { return filepath.Join(DataDir(), "schedules.json") }

// ScheduleStore 是一份定时任务清单。字段导出以便 JSON 直读直写，
// 少一层转换就少一处"存了但读不回来"的坑。
type ScheduleStore struct {
	Path  string      `json:"-"`
	Items []*Schedule `json:"items"`
	Seq   int         `json:"seq"`
	// LoadErr（v1.29）清单解析失败时的原因。解析失败绝不能静默返回空表：
	// 用户会以为"我的定时任务凭空消失了"，实际是文件坏了。
	LoadErr string `json:"-"`
	// mu 保护 Items/Seq：Save(UI 线程) vs Due/MarkRun(定时器 goroutine) 并发
	mu sync.Mutex
}

// LoadSchedules 读取清单；文件不存在或损坏时返回空清单（不报错，
// 定时任务是附属功能，配置坏了也不该拦住主程序启动）。
func LoadSchedules(path string) *ScheduleStore {
	if path == "" {
		return &ScheduleStore{Path: path}
	}
	var loaded ScheduleStore
	found, loadErr := loadStoreFile(path, "定时任务清单损坏，已备份为 %s，请重新登记（原文件保留以便排查）", &loaded)
	if !found {
		st := &ScheduleStore{Path: path}
		st.LoadErr = loadErr
		return st
	}
	loaded.Path = path
	for _, it := range loaded.Items {
		if it.NextRun.IsZero() && it.Enabled {
			it.NextRun = it.nextRun(time.Now())
		}
	}
	return &loaded
}

// Save 原子写入（MkdirAll + WriteFileAtomic，0600；已加锁）。
func (st *ScheduleStore) Save() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.saveLocked()
}

// saveLocked 已持锁版本。
func (st *ScheduleStore) saveLocked() error {
	return writeStoreAtomic(st.Path, st)
}

// Add 追加一条任务并返回它（ID 自增）。
func (st *ScheduleStore) Add(spec, prompt, workDir string, now time.Time) (*Schedule, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, err := ParseScheduleSpec(spec, prompt, workDir, now)
	if err != nil {
		return nil, err
	}
	st.Seq++
	s.ID = fmt.Sprintf("sched-%d", st.Seq)
	st.Items = append(st.Items, s)
	if err := st.saveLocked(); err != nil {
		st.Items = st.Items[:len(st.Items)-1]
		return nil, err
	}
	return s, nil
}

// Remove 删除一条任务。
func (st *ScheduleStore) Remove(id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, it := range st.Items {
		if strings.EqualFold(it.ID, id) {
			st.Items = append(st.Items[:i], st.Items[i+1:]...)
			_ = st.saveLocked()
			return true
		}
	}
	return false
}

// SetEnabled 暂停/恢复一条任务。
func (st *ScheduleStore) SetEnabled(id string, on bool, now time.Time) (*Schedule, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, it := range st.Items {
		if strings.EqualFold(it.ID, id) {
			it.Enabled = on
			if on {
				if it.EverySec > 0 {
					it.NextRun = now.Add(time.Duration(it.EverySec) * time.Second)
				} else {
					it.NextRun = it.nextRun(now)
				}
			}
			_ = st.saveLocked()
			return it, true
		}
	}
	return nil, false
}

// Due 返回此刻应当触发的任务。只取"已到点"的，不补发错过的窗口：
// 关机一夜再开机，若是补发二十次，用户看到的是界面被二十个任务同时卡死。
// 间隔类任务只触发一次，下一跳从当前时刻重算（见 MarkRun）。
func (st *ScheduleStore) Due(now time.Time) []*Schedule {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []*Schedule
	for _, it := range st.Items {
		if !it.Enabled || it.NextRun.IsZero() {
			continue
		}
		if !now.Before(it.NextRun) {
			out = append(out, it)
		}
	}
	return out
}

// DueItem 一条到点任务，附带这一跳之前漏掉了几次。
type DueItem struct {
	Schedule *Schedule
	Missed   int
}

// period 返回任务的周期，用于估算错过次数。日历类任务按名义周期算。
func (s *Schedule) period() time.Duration {
	if s.EverySec > 0 {
		return time.Duration(s.EverySec) * time.Second
	}
	// DailyAt 形如 "09:30"（每天）或 "3 09:30"（每周三）。
	if len(strings.Fields(s.DailyAt)) >= 2 {
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// DueCatchup 返回到点任务，并统计漏跑次数。
//
// 与 Due 的差别只在"告诉调用方漏了几次"：从周日关机到周一开机，
// 每 30 秒一次的任务在数学上漏了几百次，但真补起来只会把机器拖死。
// 所以仍然只跑一次，把 Missed 带出去，让提示词里写清"这是补跑，中间漏了 N 次"，
// 用户才知道该缩小统计范围而不是全量重扫。
func (st *ScheduleStore) DueCatchup(now time.Time) []DueItem {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []DueItem
	for _, it := range st.Items {
		if !it.Enabled || it.NextRun.IsZero() {
			continue
		}
		if now.Before(it.NextRun) {
			continue
		}
		missed := 1
		if p := it.period(); p > 0 {
			missed = int(now.Sub(it.NextRun)/p) + 1
			if missed > 999 {
				missed = 999
			}
			if missed < 1 {
				missed = 1
			}
		}
		out = append(out, DueItem{Schedule: it, Missed: missed})
	}
	return out
}

// MarkRun 记录一次执行，并排下一次。
func (st *ScheduleStore) MarkRun(id string, now time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, it := range st.Items {
		if strings.EqualFold(it.ID, id) {
			it.LastRun = now
			it.Runs++
			it.NextRun = it.nextRun(now)
			_ = st.saveLocked()
			return
		}
	}
}
