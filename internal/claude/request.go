package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

var SafetySettings = []map[string]string{
	{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "OFF"},
	{"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "OFF"},
	{"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT", "threshold": "OFF"},
	{"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "OFF"},
	{"category": "HARM_CATEGORY_CIVIC_INTEGRITY", "threshold": "OFF"},
}

func TransformRequest(req *ClaudeRequest, projectID string) (map[string]interface{}, error) {
	model := req.Model

	hasWebSearch := false
	for _, tool := range req.Tools {
		if tool.IsWebSearch() {
			hasWebSearch = true
			break
		}
	}

	isThinkingEnabled := false
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		isThinkingEnabled = true
	}

	if isThinkingEnabled && !strings.Contains(model, "pro") {
		log.Printf("[Claude] Thinking requested but model %s may not support it well", model)
	}

	innerRequest := make(map[string]interface{})

	contents, toolIDMap, err := buildContents(req.Messages, isThinkingEnabled)
	if err != nil {
		return nil, fmt.Errorf("failed to build contents: %v", err)
	}
	innerRequest["contents"] = contents

	if sysPrompt, _ := ParseSystemPrompt(req.System); sysPrompt != "" {
		innerRequest["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": sysPrompt},
			},
		}
	}

	innerRequest["safetySettings"] = SafetySettings

	genConfig := make(map[string]interface{})
	if req.MaxTokens != nil {
		genConfig["maxOutputTokens"] = *req.MaxTokens
	} else {
		genConfig["maxOutputTokens"] = 8192
	}
	if req.Temperature != nil {
		genConfig["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		genConfig["topP"] = *req.TopP
	}
	if req.TopK != nil {
		genConfig["topK"] = *req.TopK
	}

	if isThinkingEnabled {
		budgetTokens := 10000
		if req.Thinking.BudgetTokens != nil {
			budgetTokens = *req.Thinking.BudgetTokens
		}
		genConfig["thinkingConfig"] = map[string]interface{}{
			"thinkingBudget": budgetTokens,
		}
	}

	innerRequest["generationConfig"] = genConfig

	tools, err := buildTools(req.Tools, toolIDMap)
	if err != nil {
		return nil, fmt.Errorf("failed to build tools: %v", err)
	}
	if tools != nil {
		innerRequest["tools"] = tools
		innerRequest["toolConfig"] = map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{
				"mode": "AUTO",
			},
		}
	}

	if hasWebSearch && tools == nil {
		innerRequest["tools"] = []map[string]interface{}{
			{"googleSearch": map[string]interface{}{}},
		}
	}

	requestBody := map[string]interface{}{
		"project":     projectID,
		"requestId":   fmt.Sprintf("claude-%d", time.Now().UnixNano()),
		"model":       model,
		"userAgent":   "gemini-web2api",
		"requestType": "agent",
		"request":     innerRequest,
	}

	return requestBody, nil
}

func buildContents(messages []Message, isThinkingEnabled bool) ([]map[string]interface{}, map[string]string, error) {
	var contents []map[string]interface{}
	toolIDMap := make(map[string]string)
	var lastSignature string

	for _, msg := range messages {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}

		var parts []map[string]interface{}

		blocks, strContent, err := ParseMessageContent(msg.Content)
		if err != nil {
			return nil, nil, err
		}

		if strContent != "" {
			if strings.TrimSpace(strContent) != "" {
				parts = append(parts, map[string]interface{}{"text": strContent})
			}
		} else {
			for _, block := range blocks {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						parts = append(parts, map[string]interface{}{"text": block.Text})
					}

				case "thinking":
					if !isThinkingEnabled {
						if block.Thinking != "" {
							parts = append(parts, map[string]interface{}{"text": block.Thinking})
						}
						continue
					}

					if block.Thinking == "" {
						parts = append(parts, map[string]interface{}{"text": "..."})
						continue
					}

					part := map[string]interface{}{
						"text":    block.Thinking,
						"thought": true,
					}
					if block.Signature != "" {
						part["thoughtSignature"] = block.Signature
						lastSignature = block.Signature
					}
					parts = append(parts, part)

				case "image":
					if block.Source != nil && block.Source.Type == "base64" {
						parts = append(parts, map[string]interface{}{
							"inlineData": map[string]interface{}{
								"mimeType": block.Source.MediaType,
								"data":     block.Source.Data,
							},
						})
					}

				case "document":
					if block.Source != nil && block.Source.Type == "base64" {
						parts = append(parts, map[string]interface{}{
							"inlineData": map[string]interface{}{
								"mimeType": block.Source.MediaType,
								"data":     block.Source.Data,
							},
						})
					}

				case "tool_use":
					toolIDMap[block.ID] = block.Name
					part := map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": block.Name,
							"args": block.Input,
							"id":   block.ID,
						},
					}
					if block.Signature != "" {
						part["thoughtSignature"] = block.Signature
						lastSignature = block.Signature
					} else if lastSignature != "" {
						part["thoughtSignature"] = lastSignature
					}
					parts = append(parts, part)

				case "tool_result":
					funcName := toolIDMap[block.ToolUseID]
					if funcName == "" {
						funcName = block.ToolUseID
					}

					var contentStr string
					if block.Content != nil {
						var str string
						if json.Unmarshal(block.Content, &str) == nil {
							contentStr = str
						} else {
							var contentBlocks []map[string]interface{}
							if json.Unmarshal(block.Content, &contentBlocks) == nil {
								var texts []string
								for _, cb := range contentBlocks {
									if t, ok := cb["text"].(string); ok {
										texts = append(texts, t)
									}
								}
								contentStr = strings.Join(texts, "\n")
							} else {
								contentStr = string(block.Content)
							}
						}
					}

					if strings.TrimSpace(contentStr) == "" {
						if block.IsError != nil && *block.IsError {
							contentStr = "Tool execution failed with no output."
						} else {
							contentStr = "Command executed successfully."
						}
					}

					part := map[string]interface{}{
						"functionResponse": map[string]interface{}{
							"name":     funcName,
							"response": map[string]interface{}{"result": contentStr},
							"id":       block.ToolUseID,
						},
					}
					if lastSignature != "" {
						part["thoughtSignature"] = lastSignature
					}
					parts = append(parts, part)

				case "redacted_thinking":
					parts = append(parts, map[string]interface{}{
						"text": fmt.Sprintf("[Redacted Thinking: %s]", block.Data),
					})
				}
			}
		}

		if len(parts) == 0 {
			continue
		}

		contents = append(contents, map[string]interface{}{
			"role":  role,
			"parts": parts,
		})
	}

	return contents, toolIDMap, nil
}

func buildTools(tools []Tool, toolIDMap map[string]string) ([]map[string]interface{}, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	var functionDeclarations []map[string]interface{}
	hasGoogleSearch := false

	for _, tool := range tools {
		if tool.IsWebSearch() {
			hasGoogleSearch = true
			continue
		}

		if tool.Name == nil {
			continue
		}

		inputSchema := make(map[string]interface{})
		if tool.InputSchema != nil {
			if err := json.Unmarshal(tool.InputSchema, &inputSchema); err != nil {
				inputSchema = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				}
			}
		} else {
			inputSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		cleanJSONSchema(inputSchema)

		decl := map[string]interface{}{
			"name":       *tool.Name,
			"parameters": inputSchema,
		}
		if tool.Description != nil {
			decl["description"] = *tool.Description
		}

		functionDeclarations = append(functionDeclarations, decl)
	}

	if len(functionDeclarations) == 0 && hasGoogleSearch {
		return []map[string]interface{}{
			{"googleSearch": map[string]interface{}{}},
		}, nil
	}

	if len(functionDeclarations) > 0 {
		return []map[string]interface{}{
			{"functionDeclarations": functionDeclarations},
		}, nil
	}

	return nil, nil
}

func cleanJSONSchema(schema map[string]interface{}) {
	blacklist := []string{"$schema", "additionalProperties", "default", "examples", "x-", "definitions", "$ref", "$defs"}

	for _, key := range blacklist {
		delete(schema, key)
	}

	for key := range schema {
		for _, prefix := range []string{"x-", "$"} {
			if strings.HasPrefix(key, prefix) {
				delete(schema, key)
			}
		}
	}

	if props, ok := schema["properties"].(map[string]interface{}); ok {
		for _, prop := range props {
			if propMap, ok := prop.(map[string]interface{}); ok {
				cleanJSONSchema(propMap)
			}
		}
	}

	if items, ok := schema["items"].(map[string]interface{}); ok {
		cleanJSONSchema(items)
	}
}
