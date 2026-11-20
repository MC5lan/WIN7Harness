package dedupe

// Unique 返回去重后的切片，保留首次出现顺序。
func Unique(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		out = append(out, s)
		seen[s] = true
	}
	return out
}
