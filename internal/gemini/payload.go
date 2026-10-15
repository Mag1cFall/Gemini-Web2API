package gemini

import (
	"fmt"
)

// buildGeneratePayload 按当前官网 99 槽协议清单构造请求，modelSwitched 表示本轮模型与上一轮或账号默认模型不同
func buildGeneratePayload(request GenerateRequest, language string, preferences Preferences, requestID, contextID string, temporaryChat, modelSwitched bool) (string, error) {
	inner := make([]any, 99)

	attachments := any(nil)
	if len(request.Files) > 0 {
		values := make([]any, 0, len(request.Files))
		for _, file := range request.Files {
			values = append(values, []any{[]any{file.URL, 1, nil, file.MimeType, file.FileID}, file.FileName, nil, nil, nil, nil, nil, nil, []any{0}})
		}
		attachments = values
	}
	message := []any{request.Prompt, 0, nil, attachments, nil, nil, 0}
	switch {
	case request.Feature == FeatureVideo:
		orientation := 1
		if request.tools != nil && request.tools.aspectOrder > 0 {
			orientation = request.tools.aspectOrder
		}
		message = append(message, nil, nil, []any{nil, nil, nil, nil, nil, nil, []any{[]any{nil, nil, nil, orientation}}})
	case request.Feature == FeatureDeepResearch:
		message = append(message, nil, nil, []any{nil, nil, nil, nil, nil, nil, []any{}})
	case request.tools != nil && request.tools.aspectRatio != "":
		message = append(message, nil, nil, []any{nil, nil, nil, nil, nil, nil, []any{nil, []any{nil, request.tools.aspectRatio}}})
	}
	inner[0] = message
	inner[1] = []any{language}
	conversation := ConversationSnapshot{}
	if request.Conversation != nil {
		conversation = request.Conversation.Snapshot()
	}
	inner[17] = []any{[]any{conversation.Turns}}
	switch {
	case conversation.CID != "" && request.Branch:
		inner[2] = []any{conversation.CID, conversation.RID, conversation.RCID, nil, nil, nil, nil, nil, nil, ""}
		inner[72] = 2
	case conversation.CID != "":
		token := conversation.Token
		if temporaryChat {
			token = ""
		}
		inner[71] = []any{conversation.CID, nil, nil, nil, nil, nil, nil, token}
	}
	inner[4] = contextID
	inner[6] = []any{0}
	inner[7] = 1
	inner[10] = 1
	inner[11] = 0
	inner[18] = 0
	if request.Gem != "" {
		inner[19] = request.Gem
	}
	inner[27] = 1
	inner[30] = []any{4, 16, 19}
	inner[41] = []any{2}
	if preferences.Bool(preferenceSlot41) {
		inner[41] = []any{1}
	}
	inner[49] = int(request.Feature)
	if request.tools != nil {
		selected := make([]any, 0, len(request.tools.options))
		for _, option := range request.tools.options {
			selected = append(selected, []any{option})
		}
		inner[54] = []any{}
		if len(request.tools.sources) > 0 {
			sources := make([]any, 0, len(request.tools.sources))
			for _, option := range request.tools.sources {
				sources = append(sources, option)
			}
			inner[54] = []any{[]any{[]any{[]any{sources}}}}
		}
		inner[55] = selected
	}
	inner[53] = 0
	inner[59] = requestID
	inner[61] = []any{}
	if request.personalIntelligence {
		inner[67] = 0
	}
	inner[68] = 1
	inner[79] = request.ModelMode
	if request.ThinkingLevel != 0 {
		inner[80] = int(request.ThinkingLevel)
	}
	inner[91] = 0
	if preferences.Bool(preferenceSlot91) {
		inner[91] = 1
	}
	inner[96] = 0
	if modelSwitched {
		inner[96] = 1
	}
	inner[98] = 1
	if temporaryChat {
		inner[45] = 1
	}
	if request.researchConfirm {
		inner[67] = nil
		inner[97] = []any{0, 0}
	}

	encodedInner, err := pageJSON(inner)
	if err != nil {
		return "", fmt.Errorf("encode protocol envelope: %w", err)
	}
	outer, err := pageJSON([]any{nil, encodedInner})
	if err != nil {
		return "", fmt.Errorf("encode f.req: %w", err)
	}
	return outer, nil
}
