package netbridge

import "net/url"

// ProxyResolver 由上层注入：给定目标 URL，返回上游代理；nil 表示直连。
// 这样 netbridge 不依赖 llm/core 的配置读取，避免循环依赖。
type ProxyResolver func(target *url.URL) (*url.URL, error)
