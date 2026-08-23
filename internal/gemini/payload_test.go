package gemini

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestBuildGeneratePayload 验证 97 槽载荷和动态字段
func TestBuildGeneratePayload(t *testing.T) {
	state := NewConversationStateFrom(ConversationSnapshot{CID: "c_1", RID: "r_1", RCID: "rc_1"})
	payload, err := buildGeneratePayload(GenerateRequest{
		Prompt:          "hello",
		ModelMode:       3,
		ThinkingMode:    ThinkingExtended,
		Files:           []FileData{{URL: "file-id", FileName: "photo.png"}},
		Conversation:    state,
		ImageGeneration: true,
	}, "en", "REQUEST-ID", true)
	if err != nil {
		t.Fatal(err)
	}
	var outer []any
	if err := json.Unmarshal([]byte(payload), &outer); err != nil {
		t.Fatal(err)
	}
	if len(outer) != 2 {
		t.Fatalf("outer length = %d", len(outer))
	}
	encodedInner, ok := outer[1].(string)
	if !ok {
		t.Fatal("inner payload is not encoded JSON")
	}
	var inner []any
	if err := json.Unmarshal([]byte(encodedInner), &inner); err != nil {
		t.Fatal(err)
	}
	if len(inner) != 97 {
		t.Fatalf("inner length = %d", len(inner))
	}
	nonNull := make([]int, 0)
	for index, value := range inner {
		if value != nil {
			nonNull = append(nonNull, index)
		}
	}
	wantNonNull := []int{0, 1, 2, 6, 7, 10, 11, 17, 18, 27, 30, 41, 45, 49, 53, 59, 61, 67, 68, 79, 80, 91, 96}
	if !reflect.DeepEqual(nonNull, wantNonNull) {
		t.Fatalf("nonnull indexes = %v", nonNull)
	}
	message, _ := arrayAt(inner, 0)
	if prompt, _ := stringAt(message, 0); prompt != "hello" {
		t.Fatalf("prompt = %q", prompt)
	}
	attachmentURL, ok := stringPath(message, 3, 0, 0, 0)
	if !ok || attachmentURL != "file-id" {
		t.Fatalf("attachment URL = %q", attachmentURL)
	}
	conversation, _ := arrayAt(inner, 2)
	if cid, _ := stringAt(conversation, 0); cid != "c_1" {
		t.Fatalf("CID = %q", cid)
	}
	if requestID, _ := stringAt(inner, 59); requestID != "REQUEST-ID" {
		t.Fatalf("request ID = %q", requestID)
	}
	if inner[3] != nil || inner[4] != nil {
		t.Fatal("dynamic proof slots must not contain captured values")
	}
	if temporary, _ := intAt(inner, 45); temporary != 1 {
		t.Fatalf("temporary chat = %v", inner[45])
	}
	if imageMode, _ := intAt(inner, 49); imageMode != 14 {
		t.Fatalf("image generation mode = %v", inner[49])
	}
	if modelMode, _ := intAt(inner, 79); modelMode != 3 {
		t.Fatalf("model mode = %v", inner[79])
	}
	if thinkingMode, _ := intAt(inner, 80); thinkingMode != 2 {
		t.Fatalf("thinking mode = %v", inner[80])
	}
	temporaryMode, _ := arrayAt(inner, 6)
	if mode, _ := intAt(temporaryMode, 0); mode != 1 {
		t.Fatalf("temporary mode = %v", inner[6])
	}
	if historyMode, _ := intAt(inner, 67); historyMode != 0 {
		t.Fatalf("history mode = %v", inner[67])
	}
	if sessionMode, _ := intAt(inner, 68); sessionMode != 2 {
		t.Fatalf("session mode = %v", inner[68])
	}
	if persistenceMode, _ := intAt(inner, 96); persistenceMode != 0 {
		t.Fatalf("persistence mode = %v", inner[96])
	}
}
