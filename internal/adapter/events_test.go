package adapter

import (
	"testing"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

func TestEventAccumulatorAppliesSnapshotOperations(t *testing.T) {
	accumulator := NewEventAccumulator()
	events := []gemini.Event{
		{Kind: gemini.EventText, Candidate: 0, Operation: gemini.SnapshotAppend, Delta: "alpha", Snapshot: "alpha"},
		{Kind: gemini.EventText, Candidate: 0, Operation: gemini.SnapshotReplace, Delta: "beta", Snapshot: "beta", PrefixLength: 0},
		{Kind: gemini.EventThought, Candidate: 0, Operation: gemini.SnapshotAppend, Delta: "think", Snapshot: "think"},
		{Kind: gemini.EventThought, Candidate: 0, Operation: gemini.SnapshotTruncate, Snapshot: ""},
	}
	for _, event := range events {
		if err := accumulator.Apply(event); err != nil {
			t.Fatalf("应用事件失败: %v", err)
		}
	}
	primary := accumulator.Primary()
	if primary.Text != "beta" || primary.Thought != "" {
		t.Fatalf("快照状态异常: %+v", primary)
	}
}
