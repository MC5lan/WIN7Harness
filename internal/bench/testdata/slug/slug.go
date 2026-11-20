package slug

import "strings"

// Slug 把标题转成 URL 片段。
func Slug(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "-")
}
