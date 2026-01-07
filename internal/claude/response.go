package claude

import (
	"fmt"
	"time"
)

func TransformResponse(geminiResp *GeminiResponse, requestModel string) (*ClaudeResponse, error) {
	if geminiResp == nil {
		return nil, fmt.Errorf("nil gemini response")
	}

	response := &ClaudeResponse{
		ID:    fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Type:  "message",
		Role:  "assistant",
		Model: requestModel,
	}

	var contentBlocks []ContentBlock
	stopReason := "end_turn"

	if len(geminiResp.Candidates) > 0 {
		candidate := geminiResp.Candidates[0]

		if candidate.FinishReason != nil {
			switch *candidate.FinishReason {
			case "STOP", "MAX_TOKENS":
				stopReason = "end_turn"
			case "SAFETY":
				stopReason = "stop_sequence"
			case "RECITATION":
				stopReason = "stop_sequence"
			case "TOOL_USE":
				stopReason = "tool_use"
			default:
				stopReason = "end_turn"
			}
		}

		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Thought != nil && *part.Thought && part.Text != nil {
					block := ContentBlock{
						Type:     "thinking",
						Thinking: *part.Text,
					}
					if part.ThoughtSignature != nil {
						block.Signature = *part.ThoughtSignature
					}
					contentBlocks = append(contentBlocks, block)
				} else if part.Text != nil && *part.Text != "" {
					contentBlocks = append(contentBlocks, ContentBlock{
						Type: "text",
						Text: *part.Text,
					})
				} else if part.FunctionCall != nil {
					stopReason = "tool_use"
					id := ""
					if part.FunctionCall.ID != nil {
						id = *part.FunctionCall.ID
					} else {
						id = fmt.Sprintf("toolu_%d", time.Now().UnixNano())
					}
					contentBlocks = append(contentBlocks, ContentBlock{
						Type:  "tool_use",
						ID:    id,
						Name:  part.FunctionCall.Name,
						Input: part.FunctionCall.Args,
					})
				}
			}
		}

		if candidate.GroundingMetadata != nil && len(candidate.GroundingMetadata.GroundingChunks) > 0 {
			toolUseID := fmt.Sprintf("srvtoolu_%d", time.Now().UnixNano())

			query := ""
			if len(candidate.GroundingMetadata.WebSearchQueries) > 0 {
				query = candidate.GroundingMetadata.WebSearchQueries[0]
			}

			contentBlocks = append(contentBlocks, ContentBlock{
				Type: "server_tool_use",
				ID:   toolUseID,
				Name: "web_search",
				Input: map[string]interface{}{
					"query": query,
				},
			})
		}
	}

	if len(contentBlocks) == 0 {
		contentBlocks = append(contentBlocks, ContentBlock{
			Type: "text",
			Text: "",
		})
	}

	response.Content = contentBlocks
	response.StopReason = stopReason

	inputTokens := 0
	outputTokens := 0
	if geminiResp.UsageMetadata != nil {
		if geminiResp.UsageMetadata.PromptTokenCount != nil {
			inputTokens = *geminiResp.UsageMetadata.PromptTokenCount
		}
		if geminiResp.UsageMetadata.CandidatesTokenCount != nil {
			outputTokens = *geminiResp.UsageMetadata.CandidatesTokenCount
		}
	}

	response.Usage = Usage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}

	if geminiResp.UsageMetadata != nil && geminiResp.UsageMetadata.CachedContentTokenCount != nil {
		cachedTokens := *geminiResp.UsageMetadata.CachedContentTokenCount
		response.Usage.CacheReadInputTokens = &cachedTokens
	}

	return response, nil
}

func MapFinishReason(geminiReason string) string {
	switch geminiReason {
	case "STOP":
		return "end_turn"
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY":
		return "stop_sequence"
	case "RECITATION":
		return "stop_sequence"
	case "TOOL_USE":
		return "tool_use"
	default:
		return "end_turn"
	}
}
