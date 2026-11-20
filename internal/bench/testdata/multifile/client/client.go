package client

import "multifile/config"

// Client 持有规范化后的配置。
type Client struct {
	cfg config.Config
}

// New 构造客户端。
func New(cfg config.Config) *Client {
	return &Client{cfg: cfg}
}

// Attempts 返回最大尝试次数。
func (c *Client) Attempts() int { return c.cfg.Retries }

// Timeout 返回超时秒数。
func (c *Client) Timeout() int { return c.cfg.TimeoutSec }
