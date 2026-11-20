package config

// Config 是客户端配置。
type Config struct {
	Retries    int
	TimeoutSec int
}

// Normalize 修正非法值，返回一份可用的配置。
func (c Config) Normalize() Config {
	if c.Retries < 0 {
		c.Retries = 0
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 30
	}
	return c
}
