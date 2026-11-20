package core

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// 设备表：v1.28 的手机 token 只活在内存 map 里，进程一重启全掉，
// 配置里写的 TokenTTL=7 天形同虚设；也没有"哪些设备连过我、怎么踢掉它"。
// 这里给 token 一个落盘的家：只存哈希，不存明文；支持命名、吊销、过期清理。

// Device 一台已配对的手机。
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`

	// Tok 仅进程内使用的明文 token，绝不落盘（json:"-"）。
	// 用途：吊销设备时顺手把内存里的 token 一起清掉，
	// 否则"已下线"的手机在本次进程存活期间照样能发指令。
	Tok string `json:"-"`
}

// Expired 是否已过期。
func (d Device) Expired(now time.Time) bool {
	return !d.ExpiresAt.IsZero() && now.After(d.ExpiresAt)
}

// HashToken 对 token 取不可逆指纹。落盘只存这个值。
func HashToken(t string) string {
	sum := sha256.Sum256([]byte("win7harness|" + t))
	return hex.EncodeToString(sum[:16])
}

// DeviceStore 设备表，落盘为一个 JSON 文件。
type DeviceStore struct {
	Path    string   `json:"-"`
	Items   []Device `json:"items"`
	LoadErr string   `json:"-"`

	// mu 保护 Items：Verify/Add/Revoke/Prune 可能被手机请求的
	// HTTP goroutine 并发调用，不加锁就是数据竞争。
	mu sync.Mutex
}

// DevicesPath 默认设备表路径。
func DevicesPath() string { return filepath.Join(DataDir(), "devices.json") }

// LoadDevices 读取设备表。文件损坏时改名备份并返回空表，同时把原因记在 LoadErr，
// 由界面提示——不能像旧版那样静默当成"从没配对过"。
func LoadDevices(path string) *DeviceStore {
	ds := &DeviceStore{Path: path}
	if path == "" {
		return ds
	}
	var loaded DeviceStore
	found, loadErr := loadStoreFile(path, "设备表损坏，已备份为 %s，请重新扫码配对", &loaded)
	if !found {
		ds.LoadErr = loadErr
		return ds
	}
	ds.Items = loaded.Items
	return ds
}

// Save 原子写入设备表。
func (d *DeviceStore) Save() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.saveLocked()
}

// saveLocked 已持锁版本。
func (d *DeviceStore) saveLocked() error {
	if d.Path == "" {
		return nil
	}
	return writeStoreAtomic(d.Path, d)
}

// Add 注册一台新设备并返回设备信息（明文 token 由调用方下发给手机）。
func (d *DeviceStore) Add(name, token string, ttl time.Duration) (Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if token == "" {
		return Device{}, fmt.Errorf("token 为空")
	}
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	if name == "" {
		name = "未命名手机"
	}
	now := time.Now()
	dv := Device{
		ID:        fmt.Sprintf("d%x", now.UnixNano()),
		Name:      name,
		TokenHash: HashToken(token),
		CreatedAt: now,
		LastSeen:  now,
		ExpiresAt: now.Add(ttl),
		Tok:       token,
	}
	d.Items = append(d.Items, dv)
	if err := d.saveLocked(); err != nil {
		d.LoadErr = "设备表保存失败：" + err.Error()
		return dv, err
	}
	return dv, nil
}

// Verify 校验 token 是否可用；命中时刷新 LastSeen（节流落盘，避免每次 API 请求都写盘）。
func (d *DeviceStore) Verify(token string, now time.Time) (Device, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if token == "" {
		return Device{}, false
	}
	h := HashToken(token)
	// 常时比较：设备表是 token 判定的唯一点，逐字节比较在时序上泄漏匹配前缀
	for i := range d.Items {
		if subtle.ConstantTimeCompare([]byte(d.Items[i].TokenHash), []byte(h)) != 1 {
			continue
		}
		if d.Items[i].Expired(now) {
			return Device{}, false
		}
		needsSave := d.Items[i].LastSeen.IsZero() || now.Sub(d.Items[i].LastSeen) > 5*time.Minute
		d.Items[i].LastSeen = now
		d.Items[i].Tok = token
		if needsSave {
			// 5 分钟最多落一次盘：长轮询每 5-10 秒一个请求，
			// 每请求写一次 devices.json 会把小本子干成磨盘。
			_ = d.saveLocked()
		}
		return d.Items[i], true
	}
	return Device{}, false
}

// Revoke 按 ID 或名称吊销，返回被吊销的设备。
func (d *DeviceStore) Revoke(key string) (Device, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, it := range d.Items {
		if it.ID == key || it.Name == key {
			d.Items = append(d.Items[:i], d.Items[i+1:]...)
			if err := d.saveLocked(); err != nil {
				d.LoadErr = "设备吊销保存失败：" + err.Error() + "（重启后可能复活）"
			}
			return it, true
		}
	}
	return Device{}, false
}

// List 按最近使用排序返回副本。
func (d *DeviceStore) List() []Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Device, len(d.Items))
	copy(out, d.Items)
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// Prune 清掉已过期设备，返回清理数量。
func (d *DeviceStore) Prune(now time.Time) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	keep := d.Items[:0]
	removed := 0
	for _, it := range d.Items {
		if it.Expired(now) {
			removed++
			continue
		}
		keep = append(keep, it)
	}
	d.Items = keep
	if removed > 0 {
		if err := d.saveLocked(); err != nil {
			d.LoadErr = "过期设备清理保存失败：" + err.Error()
		}
	}
	return removed
}

// Quarantine 把损坏的文件改名备份（.bad-纳秒时间戳），返回备份路径。
func Quarantine(path string) (string, error) {
	stamp := time.Now().Format("20060102-150405") + "-" + fmt.Sprintf("%09d", time.Now().UnixNano()%1000000000)
	bak := path + ".bad-" + stamp
	if err := os.Rename(path, bak); err != nil {
		return "", err
	}
	return bak, nil
}

// fileExists 小工具：区分"文件不存在"与"内容坏了"。
func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

var _ = fileExists
