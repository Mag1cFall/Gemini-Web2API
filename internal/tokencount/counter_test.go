package tokencount

import "testing"

// TestMeasuredCounts 验证本地计数与官方 CountTokens 实测值一致
func TestMeasuredCounts(t *testing.T) {
	tests := []struct {
		text string
		raw  int
	}{
		{"a", 1},
		{"hello world", 2},
		{"你好，世界", 3},
		{"只输出协议探针 7F3A", 10},
		{`func main() { fmt.Println("hello") }`, 11},
		{"👩🏽‍💻🚀🙂", 6},
	}
	for _, test := range tests {
		if got := Text(test.text); got != test.raw {
			t.Fatalf("Text(%q) = %d, want %d", test.text, got, test.raw)
		}
		if got := Content(test.text); got != test.raw+1 {
			t.Fatalf("Content(%q) = %d, want %d", test.text, got, test.raw+1)
		}
	}
}
