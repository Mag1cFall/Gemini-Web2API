package adapter

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// stopCutter 按停止序列截断候选 0 的正文流：可能构成停止序列开头的末尾文字留到下一块再判断，命中后只转出命中处之前的正文
type stopCutter struct {
	sequences []string
	text      string
	sent      int
	matched   string
	// cutAt 是命中的停止序列在正文中的起点
	cutAt int
}

// newStopCutter 返回按非空停止序列截断正文的 stopCutter，没有停止序列时为 nil
func newStopCutter(sequences []string) *stopCutter {
	sequences = slices.DeleteFunc(slices.Clone(sequences), func(sequence string) bool { return sequence == "" })
	if len(sequences) == 0 {
		return nil
	}
	return &stopCutter{sequences: sequences}
}

// cut 以候选 0 的正文快照更新状态，返回本次可以转出的正文与是否在本次命中停止序列；快照改写了已转出的正文时从改写后的快照开头重新计算
func (s *stopCutter) cut(text string) (string, bool) {
	if s.matched != "" {
		return "", false
	}
	if !strings.HasPrefix(text, s.text[:s.sent]) {
		s.sent = 0
	}
	s.text = text
	end := len(text)
	for _, sequence := range s.sequences {
		if index := strings.Index(text, sequence); index >= 0 && (s.matched == "" || index < end) {
			end, s.matched, s.cutAt = index, sequence, index
		}
	}
	if s.matched == "" {
		for _, sequence := range s.sequences {
			for size := min(len(sequence)-1, len(text)); size > 0; size-- {
				if strings.HasSuffix(text, sequence[:size]) {
					end = min(end, len(text)-size)
					break
				}
			}
		}
	}
	if end <= s.sent {
		return "", s.matched != ""
	}
	delta := text[s.sent:end]
	s.sent = end
	return delta, s.matched != ""
}

// rest 返回生成结束时仍留着的末尾正文
func (s *stopCutter) rest() string {
	if s.matched != "" {
		return ""
	}
	return s.text[s.sent:]
}

// stopSequences 读取 OpenAI stop 参数，取值为字符串或字符串数组
func stopSequences(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("stop 必须是字符串或字符串数组")
	}
	return list, nil
}
