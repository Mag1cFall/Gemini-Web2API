package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
	"github.com/gin-gonic/gin"
)

// fillInMiddleInstruction 是带 suffix 的补全请求写入系统消息的要求
const fillInMiddleInstruction = "Write the text that belongs between the prompt and the suffix in the next message. Output only that text, without repeating the prompt or the suffix."

// CompletionRequest 表示 OpenAI 旧版 Completions 请求
type CompletionRequest struct {
	Model            string             `json:"model"`
	Prompt           json.RawMessage    `json:"prompt"`
	Suffix           string             `json:"suffix"`
	Stream           bool               `json:"stream"`
	StreamOptions    *ChatStreamOptions `json:"stream_options"`
	Echo             bool               `json:"echo"`
	N                *int               `json:"n"`
	BestOf           *int               `json:"best_of"`
	MaxTokens        *int               `json:"max_tokens"`
	Temperature      *float64           `json:"temperature"`
	TopP             *float64           `json:"top_p"`
	Stop             json.RawMessage    `json:"stop"`
	PresencePenalty  *float64           `json:"presence_penalty"`
	FrequencyPenalty *float64           `json:"frequency_penalty"`
	Logprobs         *int               `json:"logprobs"`
	LogitBias        json.RawMessage    `json:"logit_bias"`
	Seed             *int               `json:"seed"`
}

// completionPrompts 读取字符串、字符串数组、token 数组或 token 数组的数组形式的 prompt，token 按 Gemini 分词器还原为文本，缺省为文档起始的空串
func completionPrompts(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{""}, nil
	}
	var prompt string
	if json.Unmarshal(raw, &prompt) == nil {
		return []string{prompt}, nil
	}
	var prompts []string
	if json.Unmarshal(raw, &prompts) == nil && len(prompts) > 0 {
		return prompts, nil
	}
	var tokens []int
	if json.Unmarshal(raw, &tokens) == nil && len(tokens) > 0 {
		text, err := tokencount.Decode(tokens)
		return []string{text}, err
	}
	var batches [][]int
	if json.Unmarshal(raw, &batches) == nil && len(batches) > 0 {
		prompts = make([]string, 0, len(batches))
		for _, batch := range batches {
			text, err := tokencount.Decode(batch)
			if err != nil {
				return nil, err
			}
			prompts = append(prompts, text)
		}
		return prompts, nil
	}
	return nil, fmt.Errorf("prompt must be a string, an array of strings, an array of tokens or an array of token arrays")
}

// repeatEach 把每个 prompt 依次重复 n 次，choice 序号为 prompt 序号乘 n 加本次生成的序号
func repeatEach(prompts []string, n int) []string {
	repeated := make([]string, 0, len(prompts)*n)
	for _, prompt := range prompts {
		for range n {
			repeated = append(repeated, prompt)
		}
	}
	return repeated
}

// completionTranscript 把 prompt 与 suffix 写成发送给网页的会话
func completionTranscript(prompt string, suffix string) (string, error) {
	if suffix == "" {
		return renderTranscript([]map[string]interface{}{{"role": "user", "content": prompt}})
	}
	return renderTranscript([]map[string]interface{}{
		{"role": "system", "content": fillInMiddleInstruction},
		{"role": "user", "content": map[string]string{"prompt": prompt, "suffix": suffix}},
	})
}

// unsupportedCompletionParameters 返回网页不支持的补全采样参数
func unsupportedCompletionParameters(request CompletionRequest) []string {
	fields := unsupportedChatParameters(ChatRequest{
		MaxTokens: request.MaxTokens, Temperature: request.Temperature, TopP: request.TopP, Stop: request.Stop,
		PresencePenalty: request.PresencePenalty, FrequencyPenalty: request.FrequencyPenalty, Seed: request.Seed,
	})
	if request.Logprobs != nil {
		fields = append(fields, "logprobs")
	}
	if len(request.LogitBias) > 0 && string(request.LogitBias) != "null" {
		fields = append(fields, "logit_bias")
	}
	if request.BestOf != nil && *request.BestOf != 1 {
		fields = append(fields, "best_of")
	}
	return fields
}

// completionChoice 返回 text_completion 的一个 choice
func completionChoice(index int, text string, finishReason any) gin.H {
	return gin.H{"text": text, "index": index, "logprobs": nil, "finish_reason": finishReason}
}

// completionUsage 返回 Completions 用量
func completionUsage(usage gemini.Usage) gin.H {
	return gin.H{"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.OutputTokens(), "total_tokens": usage.PromptTokens + usage.OutputTokens()}
}

// CompletionHandler 处理 OpenAI /v1/completions：prompt 与 suffix 作为会话发送，每个 prompt 逐个生成 n 个 choice
func CompletionHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CompletionRequest
		if err := decodeRequest(c, &request); err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		prompts, promptErr := completionPrompts(request.Prompt)
		logging.FromContext(c.Request.Context()).Begin("completions", request.Model, request.Stream, len(prompts), 0)
		bindScope(c, pool, request.Model)
		if request.Model == "" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "model is required")
			return
		}
		if promptErr != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", promptErr.Error())
			return
		}
		n, ok := choiceCount(c, request.N)
		if !ok {
			return
		}
		prompts = repeatEach(prompts, n)
		if err := awaitModel(c, pool, request.Model); err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}
		sequences, err := stopSequences(request.Stop)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		setUnsupportedParameters(c, unsupportedCompletionParameters(request))

		id := randomID("cmpl-", 24, false)
		created := time.Now().Unix()
		model := config.MapModel(request.Model)
		chunk := func(choices []gin.H) gin.H {
			return gin.H{"id": id, "object": "text_completion", "created": created, "model": model, "choices": choices}
		}
		var usage gemini.Usage
		generate := func(index int, onEvent func(gemini.Event) error, hasVisibleOutput func() bool) (generationResult, error) {
			transcript, err := completionTranscript(prompts[index], request.Suffix)
			if err != nil {
				return generationResult{}, err
			}
			result, accountID, err := runGeneration(c.Request.Context(), pool, "", sessionCreate, request.Model, fmt.Sprintf("%s-%d", id, index), generationMedia{}, gemini.ThinkingLow, ToolBridge{Stop: sequences},
				func(*gemini.Client, bool) (string, []gemini.FileData, error) {
					return transcript, nil, nil
				}, onEvent, hasVisibleOutput)
			c.Set("account_id", accountID)
			if err == nil && result.Accumulator.Usage != nil {
				usage.PromptTokens += result.Accumulator.Usage.PromptTokens
				usage.CompletionTokens += result.Accumulator.Usage.CompletionTokens
				usage.ThoughtTokens += result.Accumulator.Usage.ThoughtTokens
			}
			return result, err
		}

		if !request.Stream {
			choices := make([]gin.H, 0, len(prompts))
			for index := range prompts {
				result, err := generate(index, nil, nil)
				if err != nil {
					writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
					return
				}
				if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
					writeOpenAIError(c, http.StatusBadGateway, "media_download_error", err.Error())
					return
				}
				primary := result.Accumulator.Primary()
				text := renderCandidateMarkdown(primary.Text, primary, 0)
				if request.Echo {
					text = prompts[index] + text
				}
				choices = append(choices, completionChoice(index, text, "stop"))
			}
			logging.FromContext(c.Request.Context()).SetUsage(usage.PromptTokens, usage.ThoughtTokens, usage.CompletionTokens)
			response := chunk(choices)
			response["usage"] = completionUsage(usage)
			c.JSON(http.StatusOK, response)
			return
		}

		setSSEHeaders(c)
		stream, stop := holdStream(c, openAIKeepalive)
		defer stop()
		for index := range prompts {
			if request.Echo && prompts[index] != "" {
				if err := writeSSEJSON(c.Writer, chunk([]gin.H{completionChoice(index, prompts[index], nil)})); err != nil {
					return
				}
			}
			projection := newStreamProjection(ToolBridge{}, false)
			result, err := generate(index, func(event gemini.Event) error {
				stream.release()
				return projection.project(event, func(event gemini.Event) error {
					return writeSSEJSON(c.Writer, chunk([]gin.H{completionChoice(index, event.Delta, nil)}))
				}, func(err error) error {
					return writeOpenAIStreamError(c.Writer, http.StatusBadGateway, "upstream_rewrite", err)
				})
			}, projection.hasVisibleOutput)
			if err != nil {
				if stream.reject() {
					writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
				} else if !projection.errorSent {
					_ = writeOpenAIStreamError(c.Writer, upstreamStatus(err), openAIUpstreamCode(err), streamFailure(c, err))
				}
				return
			}
			if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
				_ = writeOpenAIStreamError(c.Writer, http.StatusBadGateway, "media_download_error", streamFailure(c, err))
				return
			}
			primary := result.Accumulator.Primary()
			if projection.bufferText {
				if rest := renderCandidateMarkdown(primary.Text, primary, projection.textRunes); rest != "" {
					if err := writeSSEJSON(c.Writer, chunk([]gin.H{completionChoice(index, rest, nil)})); err != nil {
						return
					}
				}
			}
			if err := writeSSEJSON(c.Writer, chunk([]gin.H{completionChoice(index, "", "stop")})); err != nil {
				return
			}
		}
		logging.FromContext(c.Request.Context()).SetUsage(usage.PromptTokens, usage.ThoughtTokens, usage.CompletionTokens)
		if request.StreamOptions != nil && request.StreamOptions.IncludeUsage {
			final := chunk([]gin.H{})
			final["usage"] = completionUsage(usage)
			if err := writeSSEJSON(c.Writer, final); err != nil {
				return
			}
		}
		_ = writeSSEDone(c.Writer)
	}
}
