package llm

import "os"

// getEnv 包一层，便于测试替换（测试里不会改动进程环境）。
func getEnv(k string) string { return os.Getenv(k) }
