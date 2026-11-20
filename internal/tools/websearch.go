package tools

// 联网搜索：web_fetch 只能抓"已经知道地址"的页面，模型没有地址就无从下手。
// 本工具补上入口——用公开的 HTML 搜索结果页做检索，再把候选地址交给 web_fetch 精读。
//
// 设计取舍：
//   - 不依赖任何 API Key。Win7 用户多半没条件申请搜索 API，开箱即用才是硬指标；
//   - 三家引擎按顺序回退，任意一家改版或被限流都不至于整体不可用；
//   - 只抓结果列表，不自动抓正文，避免一次调用把上下文塞满、也把选择权留给模型；
//   - 解析函数与网络分离，可脱网用固定 HTML 断言，自检不依赖外网。

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"win7harness/internal/core"
)

func init() {
	register(Tool{
		Name: "web_search",
		Desc: "联网搜索：给出关键词，返回标题 / 链接 / 摘要列表，用于查最新资料、找官网、找报错出处。" +
			"拿到结果后挑最相关的地址再用 web_fetch 精读。不需要 API Key，默认走公开搜索结果页。" +
			"结果只是线索，回答前应抓取正文核实；搜不到或全部失败时如实说明，不要凭记忆编造。",
		Schema: `{
  "type":"object",
  "properties":{
    "query":{"type":"string","description":"搜索关键词，带上关键实体、版本号、错误码会更准"},
    "max_results":{"type":"integer","description":"返回几条结果，默认 8，最多 15"},
    "site":{"type":"string","description":"限定站点域名，如 github.com / go.dev，可留空"}
  },
  "required":["query"]
}`,
		Run: runWebSearch,
	})
}

const (
	webSearchDefault = 8
	webSearchMax     = 15
	// 结果页最多读 1MB：搜索页本身不大，超了基本是异常页。
	webSearchBodyLimit = 1 << 20
	webSearchTimeout   = 20 * time.Second
)

// searchHit 一条搜索结果。
type searchHit struct {
	Title   string
	URL     string
	Snippet string
}

func runWebSearch(ctx *Context, args map[string]interface{}) (Result, error) {
	q := strings.TrimSpace(Get(args, "query"))
	if q == "" {
		return Result{}, fmt.Errorf("query 为空")
	}
	n := GetInt(args, "max_results", webSearchDefault)
	if n <= 0 {
		n = webSearchDefault
	}
	if n > webSearchMax {
		n = webSearchMax
	}
	site := strings.TrimSpace(Get(args, "site"))
	query := q
	if site != "" {
		query = q + " site:" + strings.TrimPrefix(strings.TrimPrefix(site, "http://"), "https://")
	}

	if ctx.Progress != nil {
		ctx.Progress("搜索：" + query)
	}

	var lastErr error
	var hits []searchHit
	var engine string
	for _, e := range searchEngines {
		h, err := e.search(query, webSearchTimeout)
		if err != nil {
			lastErr = err
			continue
		}
		if len(h) == 0 {
			lastErr = fmt.Errorf("%s 返回空结果", e.name)
			continue
		}
		hits, engine = h, e.name
		break
	}
	if len(hits) == 0 {
		msg := "搜索失败："
		if lastErr != nil {
			msg += lastErr.Error()
		} else {
			msg += "没有可用引擎"
		}
		return Result{}, fmt.Errorf("%s\n建议：换成英文关键词重试，或直接用 web_fetch 打开已知地址", msg)
	}
	if len(hits) > n {
		hits = hits[:n]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "关键词：%s\n来源：%s\n结果：%d 条\n---\n", query, engine, len(hits))
	for i, h := range hits {
		fmt.Fprintf(&b, "%d. %s\n", i+1, h.Title)
		fmt.Fprintf(&b, "   %s\n", h.URL)
		if h.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", truncateRunes(h.Snippet, 240))
		}
	}
	b.WriteString("---\n以上仅为搜索线索，挑最相关的地址用 web_fetch 抓正文后再下结论。")
	return Result{Output: b.String(), Title: "搜索"}, nil
}

// searchEngine 一家可回退的搜索来源。
type searchEngine struct {
	name   string
	build  func(query string) string
	parse  func(html string, limit int) []searchHit
	accept string
}

var searchEngines = []searchEngine{
	{
		name: "duckduckgo",
		build: func(q string) string {
			return "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(q)
		},
		parse: parseDDGHtml,
	},
	{
		name: "duckduckgo-lite",
		build: func(q string) string {
			return "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(q)
		},
		parse: parseDDGLite,
	},
	{
		name: "bing",
		build: func(q string) string {
			return "https://www.bing.com/search?q=" + url.QueryEscape(q) + "&setlang=zh-CN"
		},
		parse: parseBing,
	},
}

func (e searchEngine) search(query string, timeout time.Duration) ([]searchHit, error) {
	addr := e.build(query)
	u, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}
	if err := checkPublicURL(u); err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: safeTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("重定向超过 5 次")
			}
			return checkPublicURL(req.URL)
		},
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 6.1; Win64; x64) Win7Harness/"+core.Version)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s 请求失败: %v", e.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s 返回 HTTP %s", e.name, resp.Status)
	}
	body, _, err := readLimited(resp.Body, webSearchBodyLimit)
	if err != nil {
		return nil, fmt.Errorf("%s 读取失败: %v", e.name, err)
	}
	return e.parse(string(body), webSearchMax), nil
}

var (
	reDDGResult = regexp.MustCompile(`(?is)<a[^>]+class="[^"]*result__a[^"]*"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	reDDGSnip   = regexp.MustCompile(`(?is)<a[^>]+class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	reDDGLite   = regexp.MustCompile(`(?is)<a[^>]+class="[^"]*result-link[^"]*"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	reDDGLiteS  = regexp.MustCompile(`(?is)<td[^>]+class="[^"]*result-snippet[^"]*"[^>]*>(.*?)</td>`)
	reBingItem  = regexp.MustCompile(`(?is)<li class="b_algo".*?</li>`)
	reBingLink  = regexp.MustCompile(`(?is)<h2[^>]*>\s*<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	reBingSnip  = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	reTagStrip  = regexp.MustCompile(`(?s)<[^>]*>`)
)

// parseDDGHtml 解析 html.duckduckgo.com 结果页。
func parseDDGHtml(page string, limit int) []searchHit {
	links := reDDGResult.FindAllStringSubmatch(page, limit)
	if len(links) == 0 {
		return nil
	}
	snips := reDDGSnip.FindAllStringSubmatch(page, len(links))
	out := make([]searchHit, 0, len(links))
	for i, m := range links {
		target := cleanDuckURL(m[1])
		title := normText(m[2])
		if target == "" || title == "" {
			continue
		}
		hit := searchHit{Title: title, URL: target}
		if i < len(snips) {
			hit.Snippet = normText(snips[i][1])
		}
		out = append(out, hit)
	}
	return out
}

// parseDDGLite 解析 lite.duckduckgo.com 结果页（结构更简单，作为备用）。
func parseDDGLite(page string, limit int) []searchHit {
	links := reDDGLite.FindAllStringSubmatch(page, limit)
	if len(links) == 0 {
		return nil
	}
	snips := reDDGLiteS.FindAllStringSubmatch(page, len(links))
	out := make([]searchHit, 0, len(links))
	for i, m := range links {
		target := cleanDuckURL(m[1])
		title := normText(m[2])
		if target == "" || title == "" {
			continue
		}
		hit := searchHit{Title: title, URL: target}
		if i < len(snips) {
			hit.Snippet = normText(snips[i][1])
		}
		out = append(out, hit)
	}
	return out
}

// parseBing 解析 bing.com 结果页。
func parseBing(page string, limit int) []searchHit {
	items := reBingItem.FindAllString(page, limit)
	if len(items) == 0 {
		return nil
	}
	out := make([]searchHit, 0, len(items))
	for _, it := range items {
		lm := reBingLink.FindStringSubmatch(it)
		if lm == nil {
			continue
		}
		hit := searchHit{Title: normText(lm[2]), URL: htmlUnescape(strings.TrimSpace(lm[1]))}
		if hit.URL == "" || hit.Title == "" || strings.HasPrefix(hit.URL, "javascript:") {
			continue
		}
		if sm := reBingSnip.FindStringSubmatch(it); sm != nil {
			hit.Snippet = normText(sm[1])
		}
		out = append(out, hit)
	}
	return out
}

// cleanDuckURL 把 DDG 的跳转链接还原成真实地址。
// 形如 //duckduckgo.com/l/?uddg=<percent-encoded>&rut=...
func cleanDuckURL(raw string) string {
	s := htmlUnescape(strings.TrimSpace(raw))
	if strings.HasPrefix(s, "//") {
		s = "https:" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	// 只放行 http(s) 目标：javascript:/data: 链接绝不能进列表（UI 以 Markdown 渲染）
	okScheme := func(v string) bool {
		lu := strings.ToLower(strings.TrimSpace(v))
		return strings.HasPrefix(lu, "http://") || strings.HasPrefix(lu, "https://")
	}
	if v := u.Query().Get("uddg"); v != "" {
		if !okScheme(v) {
			return ""
		}
		return v
	}
	// 有的版本把目标地址放在 u= 参数里
	if v := u.Query().Get("u"); okScheme(v) {
		return v
	}
	if strings.Contains(u.Host, "duckduckgo.com") {
		return ""
	}
	if !okScheme(s) {
		return ""
	}
	return s
}

// normText 去标签、解实体、压空白，得到一行可读文本。
func normText(s string) string {
	s = reTagStrip.ReplaceAllString(s, " ")
	s = htmlUnescape(s)
	return normalizeSpace(s)
}

// htmlUnescape 只处理搜索结果页会出现的少量实体，避免为它拉一整套解析。
func htmlUnescape(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	return decodeEntities(s)
}

// WebSearchEngines 返回可用引擎名，供 /help 与自检展示。
func WebSearchEngines() []string {
	out := make([]string, 0, len(searchEngines))
	for _, e := range searchEngines {
		out = append(out, e.name)
	}
	return out
}

// ParseSearchPageForTest 暴露解析入口给自检：按引擎名解析一段固定 HTML。
func ParseSearchPageForTest(engine, page string, limit int) []searchHit {
	for _, e := range searchEngines {
		if e.name == engine {
			if limit <= 0 {
				limit = webSearchDefault
			}
			return e.parse(page, limit)
		}
	}
	return nil
}
