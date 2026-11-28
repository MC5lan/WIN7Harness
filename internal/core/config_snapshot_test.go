package core

import "testing"

func TestConfigCloneIsDeep(t *testing.T) {
	c := DefaultConfig()
	c.Providers[0].Models = []string{"a"}
	c.KeyBindings = map[string]string{"x": "y"}
	c.PermRules = []string{"allow:run_command:go *"}
	c.MCPServers = []MCPServer{{Name: "m", Args: []string{"a"}, Env: []string{"K=V"}, Headers: []string{"H: V"}}}

	cp := c.Clone()
	cp.Providers[0].Models[0] = "b"
	cp.KeyBindings["x"] = "z"
	cp.PermRules[0] = "deny"
	cp.MCPServers[0].Args[0] = "b"
	cp.MCPServers[0].Env[0] = "K=W"
	cp.MCPServers[0].Headers[0] = "H: W"

	if c.Providers[0].Models[0] != "a" || c.KeyBindings["x"] != "y" ||
		c.PermRules[0] != "allow:run_command:go *" ||
		c.MCPServers[0].Args[0] != "a" || c.MCPServers[0].Env[0] != "K=V" ||
		c.MCPServers[0].Headers[0] != "H: V" {
		t.Fatal("Clone 不是深拷贝")
	}
}

// Get 返回已发布快照；之后修改原配置副本不能影响读者。
func TestPublishedConfigIsImmutable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorkDir = `C:\one`
	PublishConfig(cfg)
	cfg.WorkDir = `C:\two`
	if got := Get().WorkDir; got != `C:\one` {
		t.Fatalf("Get() 看到了未发布的修改：%q", got)
	}
}

// Load 返回 UI 私有副本，同时把深拷贝发布给工具 goroutine。
func TestLoadReturnsPrivateCopy(t *testing.T) {
	resetConfigLoadState(t)
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	cfg := Load()
	if cfg == nil {
		t.Fatal("Load() 返回 nil")
	}
	cfg.WorkDir = `C:\private-change`
	if got := Get().WorkDir; got == `C:\private-change` {
		t.Fatal("修改 Load 返回值污染了已发布快照")
	}
}
