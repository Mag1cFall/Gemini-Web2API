package adapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
	"github.com/gin-gonic/gin"
)

// speechChunkBytes 是 stream_format=sse 时每个 speech.audio.delta 携带的音频字节数
const speechChunkBytes = 32 << 10

// speechRequest 是 OpenAI /v1/audio/speech 请求
type speechRequest struct {
	Model          string          `json:"model"`
	Input          string          `json:"input"`
	Voice          json.RawMessage `json:"voice"`
	Instructions   string          `json:"instructions"`
	ResponseFormat string          `json:"response_format"`
	Speed          *float64        `json:"speed"`
	StreamFormat   string          `json:"stream_format"`
}

// SpeechHandler 处理 OpenAI /v1/audio/speech：以官网回答菜单的 Listen 朗读合成音频，model 选择执行朗读的账号
func SpeechHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request speechRequest
		if err := decodeRequest(c, &request); err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		trace := logging.FromContext(c.Request.Context())
		trace.Begin("speech", request.Model, request.StreamFormat == "sse", 1, 0)
		bindScope(c, pool, request.Model)
		if request.Model == "" || request.Input == "" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "model and input are required")
			return
		}
		if length := utf8.RuneCountInString(request.Input); length > gemini.SpeechTextLimit {
			writeOpenAIError(c, http.StatusBadRequest, "string_above_max_length", fmt.Sprintf(
				"Invalid 'input': string too long. Expected a string with maximum length %d, but got a string with length %d instead.", gemini.SpeechTextLimit, length))
			return
		}
		format := request.ResponseFormat
		if format == "" {
			format = "mp3"
		}
		if _, ok := audioContentTypes[format]; !ok || format == "pcm16" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf(
				"response_format %q is not available: Gemini Web read-aloud returns MP3 and Ogg Opus audio, which this service returns as mp3, opus, aac, wav, flac or pcm", format))
			return
		}
		if request.StreamFormat != "" && request.StreamFormat != "audio" && request.StreamFormat != "sse" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("stream_format must be audio or sse, got %q", request.StreamFormat))
			return
		}
		if err := awaitModel(c, pool, request.Model); err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}
		var unsupported []string
		if len(request.Voice) > 0 && string(request.Voice) != "null" {
			unsupported = append(unsupported, "voice")
		}
		if request.Instructions != "" {
			unsupported = append(unsupported, "instructions")
		}
		if request.Speed != nil && *request.Speed != 1 {
			unsupported = append(unsupported, "speed")
		}
		setUnsupportedParameters(c, unsupported)

		modelID := config.MapModel(request.Model)
		trace.SetStage("等待账号")
		excludedAccountIDs := make(map[string]struct{})
		availableAccounts := pool.Size()
		if availableAccounts == 0 {
			writeOpenAIError(c, http.StatusServiceUnavailable, "upstream_error", "No account is ready for the requested model")
			return
		}
		maxAttempts := 1
		if AccountFallbackEnabled() {
			maxAttempts = availableAccounts
		}
		var lastErr error
		for attempts := 0; attempts < maxAttempts && len(excludedAccountIDs) < availableAccounts; attempts++ {
			client, accountID, release, err := pool.AcquireForModel(c.Request.Context(), "", modelID, excludedAccountIDs)
			if err != nil {
				err = cancelledError(c.Request.Context(), err)
				writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
				return
			}
			if client == nil {
				break
			}
			c.Set("account_id", accountID)
			trace.AccountReady(accountID, modelID)
			upstreamFormat := gemini.SpeechMP3
			if format == "opus" {
				upstreamFormat = gemini.SpeechOggOpus
			}
			audio, err := client.Speech(c.Request.Context(), request.Input, "", upstreamFormat)
			err = cancelledError(c.Request.Context(), err)
			if err != nil && gemini.IsAuthenticationError(err) {
				release()
				release = nil
				logging.Info(accountID, "语音生成会话失效，尝试自动重新连接一次")
				if rErr := pool.Reconnect(c.Request.Context(), accountID); rErr == nil {
					logging.Info(accountID, "自动重新连接成功，重试语音生成")
					var newRelease func()
					client, accountID, newRelease, err = pool.AcquireForModel(c.Request.Context(), "", modelID, excludedAccountIDs)
					if err == nil && client != nil {
						release = newRelease
						audio, err = client.Speech(c.Request.Context(), request.Input, "", upstreamFormat)
						err = cancelledError(c.Request.Context(), err)
					}
				}
			}
			if release != nil {
				release()
				release = nil
			}
			if err == nil {
				pool.ReportSuccess(accountID, modelID)
				contentType := audioContentTypes[format]
				if upstreamFormat == gemini.SpeechMP3 {
					if audio, contentType, err = transcodeMP3(audio, format); err != nil {
						writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
						return
					}
				}
				inputTokens := tokencount.Text(request.Input)
				outputTokens := int(math.Ceil(audioSeconds(audio) * audioTokensPerSec))
				trace.SetUsage(inputTokens, 0, outputTokens)
				trace.SetResult(modelID, "", "stop")
				if request.StreamFormat != "sse" {
					c.Data(http.StatusOK, contentType, audio)
					return
				}
				setSSEHeaders(c)
				for offset := 0; offset < len(audio); offset += speechChunkBytes {
					chunk := audio[offset:min(offset+speechChunkBytes, len(audio))]
					if err := writeSSEJSON(c.Writer, gin.H{"type": "speech.audio.delta", "audio": base64.StdEncoding.EncodeToString(chunk)}); err != nil {
						return
					}
				}
				_ = writeSSEJSON(c.Writer, gin.H{"type": "speech.audio.done"})
				return
			}
			lastErr = err
			if retryableAccountError(err) {
				pool.ReportFailure(accountID, modelID, err)
			}
			excludedAccountIDs[accountID] = struct{}{}
		}
		if lastErr != nil {
			writeOpenAIError(c, upstreamStatus(lastErr), openAIUpstreamCode(lastErr), lastErr.Error())
			return
		}
		writeOpenAIError(c, http.StatusServiceUnavailable, "upstream_error", "No account is ready for the requested model")
	}
}

// transcriptionTask 区分转写与翻译
type transcriptionTask string

const (
	taskTranscribe transcriptionTask = "transcribe"
	taskTranslate  transcriptionTask = "translate"
)

// transcriptionRequest 是 OpenAI 转写与翻译的表单字段
type transcriptionRequest struct {
	model         string
	language      string
	prompt        string
	format        string
	stream        bool
	segments      bool
	words         bool
	file          gemini.FileData
	audioDuration float64
	speakers      []knownSpeaker
}

// transcriptSegment 是带时间戳的一段转写
type transcriptSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// transcriptWord 是带时间戳的一个词
type transcriptWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// timedTranscript 是带时间戳格式要求模型返回的 JSON
type timedTranscript struct {
	Language string              `json:"language"`
	Text     string              `json:"text"`
	Segments []transcriptSegment `json:"segments"`
	Words    []transcriptWord    `json:"words"`
}

// timedTranscriptSchema 返回带时间戳转写的 JSON Schema
func timedTranscriptSchema(words bool) json.RawMessage {
	timed := func(field string) gin.H {
		return gin.H{"type": "object", "required": []string{field, "start", "end"}, "properties": gin.H{
			field: gin.H{"type": "string"}, "start": gin.H{"type": "number"}, "end": gin.H{"type": "number"},
		}}
	}
	properties := gin.H{
		"language": gin.H{"type": "string"}, "text": gin.H{"type": "string"},
		"segments": gin.H{"type": "array", "items": timed("text")},
	}
	required := []string{"language", "text", "segments"}
	if words {
		properties["words"] = gin.H{"type": "array", "items": timed("word")}
		required = append(required, "words")
	}
	schema, _ := json.Marshal(gin.H{"type": "object", "required": required, "properties": properties})
	return schema
}

// timedFormat 判断输出格式是否需要时间戳
func timedFormat(format string) bool {
	return format == "verbose_json" || format == "srt" || format == "vtt"
}

// transcriptionPrompt 返回让模型转写或翻译音频的提示词
func transcriptionPrompt(task transcriptionTask, request transcriptionRequest) string {
	var builder strings.Builder
	if task == taskTranslate {
		builder.WriteString("Translate the speech in the attached audio into English.")
	} else {
		builder.WriteString("Transcribe the speech in the attached audio verbatim in the language it is spoken.")
	}
	if request.language != "" {
		fmt.Fprintf(&builder, " The spoken language has ISO-639-1 code %q.", request.language)
	}
	if request.prompt != "" {
		fmt.Fprintf(&builder, "\nUse this text for context, spelling and style: %s", request.prompt)
	}
	if request.format == "diarized_json" {
		builder.WriteString(diarizationPrompt(request.speakers))
	} else if timedFormat(request.format) {
		builder.WriteString("\nReturn language as the lowercase English name of the spoken language, text as the full ")
		if task == taskTranslate {
			builder.WriteString("translation")
		} else {
			builder.WriteString("transcript")
		}
		builder.WriteString(", and segments as consecutive sentences with start and end times in seconds from the beginning of the audio.")
		if request.words {
			builder.WriteString(" Return words as every word with its start and end time in seconds.")
		}
	} else {
		builder.WriteString("\nOutput only the ")
		if task == taskTranslate {
			builder.WriteString("translation")
		} else {
			builder.WriteString("transcript")
		}
		builder.WriteString(" text.")
	}
	return builder.String()
}

// parseTranscriptionRequest 读取转写与翻译的 multipart 表单，srt、vtt 与 verbose_json 与官方 whisper-1 一样忽略 stream
func parseTranscriptionRequest(c *gin.Context, task transcriptionTask) (transcriptionRequest, error) {
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		return transcriptionRequest{}, err
	}
	request := transcriptionRequest{
		model: c.PostForm("model"), language: c.PostForm("language"), prompt: c.PostForm("prompt"), format: c.DefaultPostForm("response_format", "json"),
	}
	if raw := c.PostForm("stream"); raw != "" {
		stream, err := strconv.ParseBool(raw)
		if err != nil {
			return request, fmt.Errorf("stream must be a boolean")
		}
		request.stream = stream && task == taskTranscribe
	}
	granularities := append(c.PostFormArray("timestamp_granularities[]"), c.PostFormArray("timestamp_granularities")...)
	request.segments = len(granularities) == 0
	for _, granularity := range granularities {
		switch granularity {
		case "segment":
			request.segments = true
		case "word":
			request.words = true
		default:
			return request, fmt.Errorf("timestamp_granularities must contain word or segment, got %q", granularity)
		}
	}
	switch request.format {
	case "json", "text", "srt", "vtt", "verbose_json", "diarized_json":
	default:
		return request, fmt.Errorf("response_format must be one of json, text, srt, verbose_json, vtt or diarized_json, got %q", request.format)
	}
	if request.format != "verbose_json" {
		request.words = false
	}
	if timedFormat(request.format) {
		request.stream = false
	}
	header, err := c.FormFile("file")
	if err != nil {
		return request, fmt.Errorf("file is required")
	}
	file, err := header.Open()
	if err != nil {
		return request, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return request, err
	}
	if len(data) == 0 {
		return request, fmt.Errorf("file is empty")
	}
	request.file = gemini.FileData{Data: data, FileName: header.Filename, MimeType: fileUploadMIME(header.Header.Get("Content-Type"), header.Filename, data[:min(len(data), 512)])}
	request.audioDuration = audioSeconds(data)
	if request.format == "diarized_json" {
		request.speakers, err = parseKnownSpeakers(c)
	}
	return request, err
}

// TranscriptionHandler 处理 OpenAI /v1/audio/transcriptions
func TranscriptionHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return transcriptionHandler(pool, taskTranscribe)
}

// TranslationHandler 处理 OpenAI /v1/audio/translations：把音频中的语音翻译为英文
func TranslationHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return transcriptionHandler(pool, taskTranslate)
}

// transcriptionHandler 把上传的音频交给对话模型转写或翻译，并按 response_format 输出
func transcriptionHandler(pool *balancer.AccountPool, task transcriptionTask) gin.HandlerFunc {
	return func(c *gin.Context) {
		request, err := parseTranscriptionRequest(c, task)
		if c.Request.MultipartForm != nil {
			defer c.Request.MultipartForm.RemoveAll()
		}
		trace := logging.FromContext(c.Request.Context())
		protocol := "transcriptions"
		if task == taskTranslate {
			protocol = "translations"
		}
		trace.Begin(protocol, request.model, request.stream, 1, 0)
		bindScope(c, pool, request.model)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.model == "" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "model is required")
			return
		}
		if err := awaitModel(c, pool, request.model); err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}
		var unsupported []string
		for _, field := range []string{"temperature", "include[]", "chunking_strategy", "known_speaker_names[]", "known_speaker_references[]"} {
			if c.PostForm(field) != "" && (request.format != "diarized_json" || !strings.HasPrefix(field, "known_speaker_")) {
				unsupported = append(unsupported, strings.TrimSuffix(field, "[]"))
			}
		}
		setUnsupportedParameters(c, unsupported)
		trace.SetMedia(1)

		bridge := ToolBridge{}
		if timedFormat(request.format) {
			bridge.JSONSchema = timedTranscriptSchema(request.words)
		}
		files := []gemini.FileData{request.file}
		if request.format == "diarized_json" {
			bridge.JSONSchema = diarizedTranscriptSchema()
			for _, speaker := range request.speakers {
				files = append(files, speaker.file)
			}
		}
		prompt := transcriptionPrompt(task, request)
		prepare := func(*gemini.Client, bool) (string, []gemini.FileData, error) {
			return prompt, files, nil
		}
		id := randomID("transcript_", 24, true)
		if request.stream && request.format == "diarized_json" {
			streamDiarizedTranscription(c, pool, request, id, bridge, prepare)
			return
		}
		if request.stream {
			streamTranscription(c, pool, request, id, bridge, prepare)
			return
		}
		result, accountID, err := runGeneration(c.Request.Context(), pool, "", sessionCreate, request.model, id, generationMedia{}, gemini.ThinkingLow, bridge, prepare, nil, nil)
		c.Set("account_id", accountID)
		if err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}
		text := strings.TrimSpace(result.Accumulator.Primary().Text)
		if request.format == "diarized_json" {
			transcript, err := parseDiarizedTranscript(bridge, text)
			if err != nil {
				writeOpenAIError(c, http.StatusBadGateway, "output_validation_error", err.Error())
				return
			}
			c.JSON(http.StatusOK, diarizedTranscription(request, transcript, transcriptionUsage(result.Accumulator.Usage, request.file)))
			return
		}
		if !timedFormat(request.format) {
			if request.format == "text" {
				c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(text+"\n"))
				return
			}
			body := gin.H{"text": text}
			if task == taskTranscribe {
				body["usage"] = transcriptionUsage(result.Accumulator.Usage, request.file)
			}
			c.JSON(http.StatusOK, body)
			return
		}
		parsed, _, err := bridge.Parse(text)
		var transcript timedTranscript
		if err == nil {
			err = json.Unmarshal([]byte(parsed), &transcript)
		}
		if err != nil {
			writeOpenAIError(c, http.StatusBadGateway, "output_validation_error", err.Error())
			return
		}
		switch request.format {
		case "srt":
			c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(subtitles(transcript.Segments, false)))
		case "vtt":
			c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(subtitles(transcript.Segments, true)))
		default:
			c.JSON(http.StatusOK, verboseTranscription(task, request, transcript))
		}
	}
}

// streamTranscription 以 transcript.text.delta 与 transcript.text.done 事件流式输出转写
func streamTranscription(c *gin.Context, pool *balancer.AccountPool, request transcriptionRequest, id string, bridge ToolBridge, prepare func(*gemini.Client, bool) (string, []gemini.FileData, error)) {
	setSSEHeaders(c)
	defer startKeepalive(c, openAIKeepalive)()
	projection := newStreamProjection(bridge, false)
	result, accountID, err := runGeneration(c.Request.Context(), pool, "", sessionCreate, request.model, id, generationMedia{}, gemini.ThinkingLow, bridge, prepare,
		func(event gemini.Event) error {
			return projection.project(event, func(event gemini.Event) error {
				return writeSSEJSON(c.Writer, gin.H{"type": "transcript.text.delta", "delta": event.Delta})
			}, func(err error) error {
				return writeOpenAIStreamError(c.Writer, http.StatusBadGateway, "upstream_rewrite", err)
			})
		}, projection.hasVisibleOutput)
	c.Set("account_id", accountID)
	if err != nil {
		if !projection.errorSent {
			_ = writeOpenAIStreamError(c.Writer, upstreamStatus(err), openAIUpstreamCode(err), streamFailure(c, err))
		}
		return
	}
	primary := result.Accumulator.Primary()
	if projection.bufferText {
		if rest := renderCandidateMarkdown(primary.Text, primary, projection.textRunes); rest != "" {
			if err := writeSSEJSON(c.Writer, gin.H{"type": "transcript.text.delta", "delta": rest}); err != nil {
				return
			}
		}
	}
	_ = writeSSEJSON(c.Writer, gin.H{
		"type": "transcript.text.done", "text": strings.TrimSpace(primary.Text), "usage": transcriptionUsage(result.Accumulator.Usage, request.file),
	})
}

// transcriptionUsage 返回 gpt-4o-transcribe 形状的 tokens 用量，音频 token 按 Gemini 每秒 32 计
func transcriptionUsage(usage *gemini.Usage, file gemini.FileData) gin.H {
	audioTokens := mediaTokens([]gemini.FileData{file})
	input, output := audioTokens, 0
	if usage != nil {
		input, output = max(usage.PromptTokens, audioTokens), usage.OutputTokens()
	}
	return gin.H{
		"type": "tokens", "input_tokens": input, "output_tokens": output, "total_tokens": input + output,
		"input_token_details": gin.H{"text_tokens": input - audioTokens, "audio_tokens": audioTokens},
	}
}

// verboseTranscription 返回 whisper verbose_json 形状的转写结果
func verboseTranscription(task transcriptionTask, request transcriptionRequest, transcript timedTranscript) gin.H {
	duration := request.audioDuration
	if count := len(transcript.Segments); count > 0 && transcript.Segments[count-1].End > duration {
		duration = transcript.Segments[count-1].End
	}
	body := gin.H{"task": string(task), "language": strings.ToLower(transcript.Language), "duration": duration, "text": transcript.Text}
	if request.segments {
		segments := make([]gin.H, 0, len(transcript.Segments))
		for index, segment := range transcript.Segments {
			segments = append(segments, gin.H{
				"id": index, "seek": 0, "start": segment.Start, "end": segment.End, "text": segment.Text, "tokens": []int{},
				"temperature": 0, "avg_logprob": 0, "compression_ratio": 0, "no_speech_prob": 0,
			})
		}
		body["segments"] = segments
	}
	if request.words {
		body["words"] = transcript.Words
	}
	if task == taskTranscribe {
		body["usage"] = gin.H{"type": "duration", "seconds": int(math.Ceil(duration))}
	}
	return body
}

// subtitles 把转写分段写成 SRT 或 WebVTT 字幕
func subtitles(segments []transcriptSegment, vtt bool) string {
	var builder strings.Builder
	if vtt {
		builder.WriteString("WEBVTT\n\n")
	}
	for index, segment := range segments {
		if !vtt {
			fmt.Fprintf(&builder, "%d\n", index+1)
		}
		fmt.Fprintf(&builder, "%s --> %s\n%s\n\n", subtitleTime(segment.Start, vtt), subtitleTime(segment.End, vtt), strings.TrimSpace(segment.Text))
	}
	return builder.String()
}

// subtitleTime 把秒数写成字幕时间戳，SRT 毫秒分隔符为逗号，WebVTT 为句点
func subtitleTime(seconds float64, vtt bool) string {
	total := time.Duration(math.Round(max(seconds, 0)*1000)) * time.Millisecond
	separator := ","
	if vtt {
		separator = "."
	}
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", int(total.Hours()), int(total.Minutes())%60, int(total.Seconds())%60, separator, total.Milliseconds()%1000)
}
