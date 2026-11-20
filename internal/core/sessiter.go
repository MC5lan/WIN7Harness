package core

// 会话文件的流式解析器。
//
// 背景：SessionIndex / SessionLines 原来把整个会话文件 ReadFile + json.Unmarshal，
// 工具输出动辄几 MB 的会话（模型几十轮带大结果的对话）在 32 位 Win7 进程里就是 OOM 的雷，
// 且手机端每 10 秒拉一次列表，全量解析是常驻 CPU/IO 浪费。
//
// 这里用 json.Decoder 逐 token 推进：只物化摘要需要的值（id/title/at）和窗口内的消息，
// 其余字段全部 skip —— 跳过的子树不构造任何 Go 对象，内存占用与文件大小无关。

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

// maxDecompressedBytes 单会话解压后上限（与 GunzipData 默认一致）。
const maxDecompressedBytes = int64(128) << 20

// capReadCloser 计数读取器：超过上限即报错（畸形文件/压缩炸弹兜底）。
// 读取走 r（已限量），关闭走 u（含底层文件句柄，gzip 路径必须一起关）。
type capReadCloser struct {
	r   io.Reader
	u   io.Closer
	n   int64
	max int64
}

func (c *capReadCloser) Read(p []byte) (int, error) {
	if c.n >= c.max {
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(p)) > c.max-c.n {
		p = p[:c.max-c.n]
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *capReadCloser) Close() error {
	if c.u != nil {
		return c.u.Close()
	}
	return nil
}

// multiCloser 顺序关闭 gzip 流与底层文件。
type multiCloser struct {
	zr io.Closer
	f  io.Closer
}

func (m multiCloser) Close() error {
	_ = m.zr.Close()
	return m.f.Close()
}

// sessMsgLite 流式解析出的一条消息（只留摘要字段）。
type sessMsgLite struct {
	Role  string    `json:"role"`
	Text  string    `json:"text"`
	At    time.Time `json:"at"`
	Err   string    `json:"err"`
	Tools []struct {
		Name string `json:"name"`
	} `json:"tools"`
}

// sessScanResult 一次流式扫描的产物。
type sessScanResult struct {
	V        int
	ID       string
	Title    string
	At       time.Time
	Pinned   bool
	Msgs     int
	LastText string // 最后一条有文本的消息（预览用）
	// 收集窗口：from >= 0 时，保留 [from, total) 中最后 keepMax 条。
	Collected []sessMsgLite
}

// skipValue 跳过一个 JSON 值（对象/数组/标量），不构造任何 Go 值。
// 深度上限防病态嵌套。
func skipValue(dec *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON 嵌套过深，已中止")
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil // 标量：Token 已消费
	}
	if d == '{' {
		for dec.More() {
			if _, err := dec.Token(); err != nil { // key
				return err
			}
			if err := skipValue(dec, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token() // '}'
		return err
	}
	if d == '[' {
		for dec.More() {
			if err := skipValue(dec, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token() // ']'
		return err
	}
	return nil
}

// scanSessionLite 流式解析会话 JSON（gzip 先由调用方解好）。
// keepFrom=-1 时不收集消息正文；>=0 时收集 [from, ∞) 中最后 keepMax 条。
func scanSessionLite(r io.Reader, keepFrom, keepMax int) (*sessScanResult, error) {
	dec := json.NewDecoder(r)
	res := &sessScanResult{}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("会话文件外壳不是 JSON 对象")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		switch key {
		case "v":
			if err := dec.Decode(&res.V); err != nil {
				return nil, err
			}
		case "conv":
			if err := scanConvLite(dec, res, keepFrom, keepMax); err != nil {
				return nil, err
			}
		default:
			if err := skipValue(dec, 0); err != nil {
				return nil, err
			}
		}
	}
	_, err = dec.Token() // '}'
	if err != nil {
		return nil, err
	}
	return res, nil
}

func scanConvLite(dec *json.Decoder, res *sessScanResult, keepFrom, keepMax int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("会话 conv 不是对象")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := keyTok.(string)
		switch key {
		case "id":
			if err := dec.Decode(&res.ID); err != nil {
				return err
			}
		case "title":
			if err := dec.Decode(&res.Title); err != nil {
				return err
			}
		case "at":
			if err := dec.Decode(&res.At); err != nil {
				return err
			}
		case "pinned":
			if err := dec.Decode(&res.Pinned); err != nil {
				return err
			}
		case "msgs":
			if err := scanMsgsLite(dec, res, keepFrom, keepMax); err != nil {
				return err
			}
		default:
			if err := skipValue(dec, 0); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token() // '}'
	return err
}

func scanMsgsLite(dec *json.Decoder, res *sessScanResult, keepFrom, keepMax int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		// 旧版本空会话会写成 "msgs":null。语义就是 0 条消息，
		// 不要判成坏文件，否则每次启动都刷一堆“解析失败”。
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return errors.New("会话 msgs 不是数组")
	}
	idx := 0
	for dec.More() {
		var m sessMsgLite
		if err := dec.Decode(&m); err != nil {
			return err
		}
		res.Msgs++
		if t := strings.TrimSpace(m.Text); t != "" {
			res.LastText = t
		}
		if keepFrom >= 0 && idx >= keepFrom && keepMax > 0 {
			res.Collected = append(res.Collected, m)
			if len(res.Collected) > keepMax {
				res.Collected = res.Collected[len(res.Collected)-keepMax:]
			}
		}
		idx++
	}
	_, err = dec.Token() // ']'
	return err
}
