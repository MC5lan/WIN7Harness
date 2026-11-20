package core

// 配置的容错解析：一个字段坏了，只有这个字段回退默认值，其余字段照常生效。
//
// v1.29 之前的行为：json.Unmarshal 任一字段失败 → 整份配置被 Quarantine + 回退默认，
// 用户的 API Key、主题、绑定等全部丢失只因为一个数字超界（32 位进程里
// budget_tokens=3000000000、temperature=1e999 这类最常见）。
// 现在：逐字段容错解码，坏的跳过并留名单给界面播报。

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// tolerantLoad 容错解析 data 中的 Config。
// 返回 (cfg, badKeys, err)：err 非 nil 表示根本不是 JSON 对象（原逻辑的 Quarantine 路径）；
// badKeys 非空表示有被回退默认的字段路径（如 "budget_tokens"、"providers[1].api_key"）。
func tolerantLoad(data []byte) (*Config, []string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	c := DefaultConfig()
	cv := reflect.ValueOf(c).Elem()
	ct := cv.Type()
	var bad []string
	for i := 0; i < ct.NumField(); i++ {
		sf := ct.Field(i)
		tag := sf.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" || name == "" {
			continue
		}
		rm, ok := raw[name]
		if !ok {
			continue // 字段缺失：保留默认值，不算坏
		}
		field := cv.Field(i)
		if !field.CanSet() {
			continue
		}
		_ = tolerantInto(field, rm, name, &bad)
	}
	return c, bad, nil
}

// tolerantInto 把 raw 解码到 dst；叶子失败保持默认并登记 bad；
// 顶层结构（struct 位置不是对象 / slice 位置不是数组 / map 位置不是对象）坏了也放弃该子树，
// 并返回 err 供调用方丢弃该元素。叶子错误永远不上抛——一个字段坏了不拖垮字段所在的对象。
func tolerantInto(dst reflect.Value, raw json.RawMessage, path string, bad *[]string) error {
	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return tolerantInto(dst.Elem(), raw, path, bad)
	}

	switch dst.Kind() {
	case reflect.Struct:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			*bad = append(*bad, path)
			return err
		}
		st := dst.Type()
		for i := 0; i < st.NumField(); i++ {
			sf := st.Field(i)
			tag := sf.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "-" || name == "" {
				continue
			}
			rm, ok := m[name]
			if !ok {
				continue
			}
			f := dst.Field(i)
			if !f.CanSet() {
				continue
			}
			_ = tolerantInto(f, rm, path+"."+name, bad)
		}
		return nil

	case reflect.Slice:
		var rawItems []json.RawMessage
		if err := json.Unmarshal(raw, &rawItems); err != nil {
			*bad = append(*bad, path)
			return err
		}
		elT := dst.Type().Elem()
		out := reflect.MakeSlice(dst.Type(), 0, len(rawItems))
		for i, item := range rawItems {
			elem := reflect.New(elT).Elem()
			sub := fmt.Sprintf("%s[%d]", path, i)
			if err := tolerantInto(elem, item, sub, bad); err == nil {
				out = reflect.Append(out, elem)
			}
			// 元素整体结构坏了（不是对象/不是数组）：丢弃该元素，其余兄弟不受影响
		}
		dst.Set(out)
		return nil

	case reflect.Map:
		// 直接按目标类型解析一次；类型完全对不上时丢弃整个 map
		if err := json.Unmarshal(raw, dst.Addr().Interface()); err != nil {
			*bad = append(*bad, path)
			return err
		}
		return nil

	default:
		if err := json.Unmarshal(raw, dst.Addr().Interface()); err != nil {
			*bad = append(*bad, path)
		}
		// 叶子错误不上抛：字段保持默认，元素所在的对象/数组照常保留
		return nil
	}
}
