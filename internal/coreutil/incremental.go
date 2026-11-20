package coreutil

// ReusePlan 判定一组有序分段能否沿用上一轮的排版结果。
//
// prev 是上一轮每个分段的签名，cur 是这一轮算出来的签名；长度不一致时，
// 多出来的尾部必然重建，变短的直接丢弃。full 为 true 表示全局条件
// （内容宽度、字号档位、主题等）变了，此时高度与字形测量全部失效，
// 必须整段重建。
//
// 该判定本身与界面无关，因此放在 core 里单测覆盖，避免"增量排版"这种
// 容易静默出错的地方只能靠肉眼看。
func ReusePlan(prev, cur []uint64, full bool) []bool {
	out := make([]bool, len(cur))
	if full || len(prev) == 0 {
		return out
	}
	for i := range cur {
		if i < len(prev) && prev[i] == cur[i] {
			out[i] = true
		}
	}
	return out
}
