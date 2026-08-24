package gemini

import (
	"encoding/json"
	"fmt"
)

// buildGeneratePayload 从当前 97 槽协议清单构造请求
func buildGeneratePayload(request GenerateRequest, language, requestID string, temporaryChat bool) (string, error) {
	inner := make([]any, 97)

	attachments := any(nil)
	if len(request.Files) > 0 {
		values := make([]any, 0, len(request.Files))
		for _, file := range request.Files {
			values = append(values, []any{[]any{file.URL, 1}, file.FileName})
		}
		attachments = values
	}
	inner[0] = []any{request.Prompt, 0, nil, attachments, nil, nil, 0}
	inner[1] = []any{language}
	conversation := ConversationSnapshot{}
	if request.Conversation != nil {
		conversation = request.Conversation.Snapshot()
	}
	inner[2] = []any{conversation.CID, conversation.RID, conversation.RCID, nil, nil, nil, nil, nil, nil, ""}
	inner[6] = []any{0}
	inner[7] = 1
	inner[10] = 1
	inner[11] = 0
	inner[17] = []any{[]any{0}}
	inner[18] = 0
	inner[27] = 1
	inner[30] = []any{4}
	inner[41] = []any{1}
	inner[53] = 0
	inner[59] = requestID
	inner[61] = []any{}
	inner[68] = 1
	inner[79] = request.ModelMode
	inner[80] = int(request.ThinkingMode) + 1
	inner[91] = 0
	inner[96] = 1
	if temporaryChat {
		inner[6] = []any{1}
		inner[45] = 1
		inner[67] = 0
		inner[68] = 2
		inner[96] = 0
	}

	encodedInner, err := json.Marshal(inner)
	if err != nil {
		return "", fmt.Errorf("encode protocol envelope: %w", err)
	}
	outer, err := json.Marshal([]any{nil, string(encodedInner)})
	if err != nil {
		return "", fmt.Errorf("encode f.req: %w", err)
	}
	return string(outer), nil
}
