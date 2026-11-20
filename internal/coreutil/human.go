package coreutil

// core 搬迁出来的零依赖小工具：文件尺寸展示与整数转字符串。
// （core 保留自己那份给 SnapshotFile/更新器等使用；纯算法包不 back-import core，
// 否则"拆包"只剩文件移动、不解决耦合。）

// HumanSize 把字节数写成人类可读形式。
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return itoa(n) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return trimFloat(v) + " " + units[i]
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func trimFloat(v float64) string {
	s := ""
	whole := int64(v)
	frac := int64((v - float64(whole)) * 10)
	if frac == 0 {
		s = itoa(whole)
	} else {
		s = itoa(whole) + "." + itoa(frac)
	}
	return s
}
