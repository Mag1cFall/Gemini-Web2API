package gemini

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestFrameDecoderGeneratedImages 验证占位地址不会进入媒体结果
func TestFrameDecoderGeneratedImages(t *testing.T) {
	candidate := make([]any, 13)
	candidate[0] = "rc_1"
	candidate[1] = []any{"done"}
	candidate[8] = []any{2}
	candidate[12] = []any{nil, nil, nil, nil, nil, nil, nil, []any{[]any{
		[]any{[]any{nil, nil, nil, []any{nil, nil, nil, "https://googleusercontent.com/image_generation_content/0_1"}}},
		[]any{[]any{nil, nil, nil, []any{nil, nil, nil, "https://lh3.googleusercontent.com/gg-dl/preview"}}},
	}}}
	payload := []any{nil, []any{"c_1", "r_1"}, nil, nil, []any{candidate}}
	stream := ")]}'\n\n" + protocolFrame(t, payload) + "25\n[[\"e\",4,null,null,1]]\n"
	events := make([]Event, 0)
	if err := NewFrameDecoder().Decode(strings.NewReader(stream), nil, func(event Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	images := eventsOfKind(events, EventImage)
	if len(images) != 1 || images[0].Image.URL != "https://lh3.googleusercontent.com/gg-dl/preview" {
		t.Fatalf("images = %+v", images)
	}
}

type oneByteReader struct {
	value string
}

func (r *oneByteReader) Read(buffer []byte) (int, error) {
	if r.value == "" {
		return 0, io.EOF
	}
	buffer[0] = r.value[0]
	r.value = r.value[1:]
	return 1, nil
}

// TestFrameDecoderSnapshots 验证传输切片、快照修订和会话状态
func TestFrameDecoderSnapshots(t *testing.T) {
	stream := ")]}'\n\n"
	stream += protocolFrame(t, []any{nil, []any{"c_1", "r_1"}, map[string]any{"11": []any{"title"}, "21": []any{"token"}}})
	stream += protocolFrame(t, candidatePayload("c_1", "r_1", "rc_1", "Hel", "plan", 1))
	stream += protocolFrame(t, candidatePayload("c_1", "r_1", "rc_1", "Hello", "plot", 1))
	stream += protocolFrame(t, candidatePayload("c_1", "r_1", "rc_1", "Hell", "plot", 2))
	stream += "25\n[[\"e\",4,null,null,1]]\n"

	state := NewConversationState()
	events := make([]Event, 0)
	err := NewFrameDecoder().Decode(&oneByteReader{value: stream}, state, func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot(); got != (ConversationSnapshot{CID: "c_1", RID: "r_1", RCID: "rc_1"}) {
		t.Fatalf("session = %+v", got)
	}
	textEvents := eventsOfKind(events, EventText)
	if len(textEvents) != 3 {
		t.Fatalf("text events = %d", len(textEvents))
	}
	if textEvents[0].Operation != SnapshotAppend || textEvents[0].Delta != "Hel" {
		t.Fatalf("first text event = %+v", textEvents[0])
	}
	if textEvents[1].Operation != SnapshotAppend || textEvents[1].Delta != "lo" {
		t.Fatalf("second text event = %+v", textEvents[1])
	}
	if textEvents[2].Operation != SnapshotTruncate || textEvents[2].Snapshot != "Hell" {
		t.Fatalf("third text event = %+v", textEvents[2])
	}
	thoughtEvents := eventsOfKind(events, EventThought)
	if len(thoughtEvents) != 2 || thoughtEvents[1].Operation != SnapshotReplace || thoughtEvents[1].Delta != "ot" {
		t.Fatalf("thought events = %+v", thoughtEvents)
	}
	done := eventsOfKind(events, EventDone)
	if len(done) != 1 || done[0].FinishReason != FinishStop {
		t.Fatalf("done events = %+v", done)
	}
}

// TestFrameDecoderProtocolError 验证帧内错误不会被静默吞掉
func TestFrameDecoderProtocolError(t *testing.T) {
	stream := ")]}'\n\n102\n[[\"er\",null,null,null,null,400,null,null,null,3]]\n25\n[[\"e\",4,null,null,1]]\n"
	events := make([]Event, 0)
	err := NewFrameDecoder().Decode(strings.NewReader(stream), nil, func(event Event) error {
		events = append(events, event)
		return nil
	})
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.Code != 400 {
		t.Fatalf("error = %v", err)
	}
	if len(eventsOfKind(events, EventError)) != 1 {
		t.Fatalf("events = %+v", events)
	}
}

func protocolFrame(t *testing.T, payload []any) string {
	t.Helper()
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal([]any{[]any{"wrb.fr", nil, string(encodedPayload)}})
	if err != nil {
		t.Fatal(err)
	}
	return "100\n" + string(frame) + "\n"
}

func candidatePayload(cid, rid, rcid, text, thought string, phase int) []any {
	candidate := make([]any, 38)
	candidate[0] = rcid
	candidate[1] = []any{text}
	candidate[8] = []any{phase}
	candidate[37] = []any{[]any{thought}}
	return []any{nil, []any{cid, rid}, nil, nil, []any{candidate}}
}

func eventsOfKind(events []Event, kind EventKind) []Event {
	result := make([]Event, 0)
	for _, event := range events {
		if event.Kind == kind {
			result = append(result, event)
		}
	}
	return result
}
