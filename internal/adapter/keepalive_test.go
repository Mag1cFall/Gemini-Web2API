package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
)

// TestGeminiStreamHeartbeatFrames 验证 Gemini SSE 静默期间的保活不破坏 google-genai 的分帧：Go SDK 以空行切分事件，跳过空事件，其余事件必须以 data 字段开头
func TestGeminiStreamHeartbeatFrames(t *testing.T) {
	for beats := range 4 {
		t.Run(fmt.Sprint(beats), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest("POST", "/v1beta/models/m:streamGenerateContent?alt=sse", nil)
				stop := startKeepalive(c, geminiKeepalive)
				silence := time.Duration(beats)*keepaliveInterval + 1500*time.Millisecond
				for _, text := range []string{"one", "two", "three"} {
					time.Sleep(silence)
					fmt.Fprintf(c.Writer, "data: {\"text\":%q}\n\n", text)
				}
				stop()
				body := recorder.Body.Bytes()
				frames := 0
				for _, event := range bytes.Split(body, []byte("\n\n")) {
					if len(event) == 0 {
						continue
					}
					if field, _, _ := bytes.Cut(event, []byte(":")); string(field) != "data" {
						t.Fatalf("事件不以 data 字段开头: %q\nbody=%q", event, body)
					}
					frames++
				}
				if frames != 3 {
					t.Fatalf("frames=%d body=%q", frames, body)
				}
			})
		})
	}
}

// TestResponsesKeepaliveSequence 验证 Responses 流静默期间的保活是带下一个序号的 response.in_progress 事件：OpenAI Responses 流的事件 sequence_number 从 0 起逐个加一
func TestResponsesKeepaliveSequence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		stream, stop := startStreamWriter(c, openAIKeepalive, false)
		writer := responseSequenceWriter{writer: c.Writer}
		if err := writer.start("resp_1", "m", 1); err != nil {
			t.Fatal(err)
		}
		stream.setPulse(writer.progress)
		time.Sleep(2*keepaliveInterval + 1500*time.Millisecond)
		if err := writer.textDelta("hi"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(keepaliveInterval + 1500*time.Millisecond)
		stop()
		body := recorder.Body.Bytes()
		sequence, progress := 0, 0
		for _, event := range bytes.Split(body, []byte("\n\n")) {
			if len(event) == 0 {
				continue
			}
			_, payload, ok := bytes.Cut(event, []byte("\ndata: "))
			var decoded struct {
				Type     string `json:"type"`
				Sequence *int   `json:"sequence_number"`
			}
			if !ok || json.Unmarshal(payload, &decoded) != nil || decoded.Sequence == nil || *decoded.Sequence != sequence {
				t.Fatalf("第 %d 个事件: %q\nbody=%q", sequence, event, body)
			}
			if decoded.Type == "response.in_progress" {
				progress++
			}
			sequence++
		}
		if progress != 4 {
			t.Fatalf("response.in_progress 共 %d 个，应为开头 1 个加静默期间 3 个\nbody=%q", progress, body)
		}
	})
}
