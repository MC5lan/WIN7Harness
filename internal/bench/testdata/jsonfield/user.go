package jsonfield

import "encoding/json"

// User 是待编码的用户。
type User struct {
	Name  string `json:"name"`
	Email string `json:"email_address,omitempty"`
	Age   int    `json:"age"`
}

// Encode 返回用户的 JSON 表示。
func Encode(u User) (string, error) {
	b, err := json.Marshal(u)
	return string(b), err
}
