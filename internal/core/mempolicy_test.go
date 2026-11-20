package core

import "testing"

func TestMemoryLimitFor(t *testing.T) {
	const mb = int64(1 << 20)
	cases := []struct {
		totalMB int
		arch    string
		want    int64
	}{
		// 2GB 386：给 DLL/GDI/栈留余量，不能顶满 2GB。
		{2048, "386", 1200 * mb},
		// 386 大内存机器也只给 1.2GB，地址空间仍是 2GB。
		{8192, "386", 1200 * mb},
		// 探测失败时 386 也有保守上限。
		{0, "386", 1200 * mb},
		// 4GB amd64：65%。
		{4096, "amd64", 4096 * mb * 65 / 100},
		// 大内存机器封顶 6GB，避免一个进程无限吃。
		{16384, "amd64", 6144 * mb},
		// 探测失败时 amd64 按 1.5GB 兜底。
		{0, "amd64", 1536 * mb},
	}
	for _, c := range cases {
		if got := memoryLimitFor(c.totalMB, c.arch); got != c.want {
			t.Errorf("memoryLimitFor(%d,%s)=%d want %d", c.totalMB, c.arch, got, c.want)
		}
	}
}

func TestMemoryLimitForSmall386(t *testing.T) {
	const mb = int64(1 << 20)
	// 1GB Win7 Starter 一类机器：55%，不低于 512MB。
	if got := memoryLimitFor(1024, "386"); got != 1024*mb*55/100 {
		t.Fatalf("got %d", got)
	}
	// 极小内存也不能低于 512MB，否则 Go runtime 自己就转不动。
	if got := memoryLimitFor(256, "386"); got != 512*mb {
		t.Fatalf("got %d", got)
	}
}
