package adapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
)

// knownSpeaker 是 known_speaker_names[] 与 known_speaker_references[] 给出的一位说话人及其参考音频
type knownSpeaker struct {
	name string
	file gemini.FileData
}

// diarizedSegment 是模型返回的一段带说话人的转写
type diarizedSegment struct {
	Speaker string  `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
}

// diarizedTranscript 是 diarized_json 要求模型返回的 JSON
type diarizedTranscript struct {
	Text     string            `json:"text"`
	Segments []diarizedSegment `json:"segments"`
}

// diarizedTranscriptSchema 返回带说话人分段转写的 JSON Schema
func diarizedTranscriptSchema() json.RawMessage {
	schema, _ := json.Marshal(gin.H{"type": "object", "required": []string{"text", "segments"}, "properties": gin.H{
		"text": gin.H{"type": "string"},
		"segments": gin.H{"type": "array", "items": gin.H{"type": "object", "required": []string{"speaker", "start", "end", "text"}, "properties": gin.H{
			"speaker": gin.H{"type": "string"}, "start": gin.H{"type": "number"}, "end": gin.H{"type": "number"}, "text": gin.H{"type": "string"},
		}}},
	}})
	return schema
}

// parseKnownSpeakers 读取 known_speaker_names[] 与 known_speaker_references[]，参考音频为 data URL
func parseKnownSpeakers(c *gin.Context) ([]knownSpeaker, error) {
	names := append(c.PostFormArray("known_speaker_names[]"), c.PostFormArray("known_speaker_names")...)
	references := append(c.PostFormArray("known_speaker_references[]"), c.PostFormArray("known_speaker_references")...)
	if len(names) != len(references) {
		return nil, fmt.Errorf("known_speaker_names[] and known_speaker_references[] must have the same length, got %d and %d", len(names), len(references))
	}
	speakers := make([]knownSpeaker, 0, len(names))
	for index, name := range names {
		mimeType, encoded, ok := splitDataURL(references[index])
		data, err := base64.StdEncoding.DecodeString(encoded)
		if !ok || err != nil || len(data) == 0 {
			return nil, fmt.Errorf("known_speaker_references[%d] must be a base64 data URL of an audio sample", index)
		}
		speakers = append(speakers, knownSpeaker{name: name, file: gemini.FileData{Data: data, FileName: fmt.Sprintf("speaker-%d%s", index+1, mimeTypeToExt(mimeType)), MimeType: mimeType}})
	}
	return speakers, nil
}

// diarizationPrompt 返回要求模型按声音分段的提示，给出参考音频时按附件顺序说明每段参考录音是谁
func diarizationPrompt(speakers []knownSpeaker) string {
	var builder strings.Builder
	builder.WriteString("\nReturn only a JSON object whose \"text\" field is the full transcript and whose \"segments\" field lists consecutive sentences, each with \"speaker\", \"start\" and \"end\" times in seconds from the beginning of the audio, and \"text\"; ")
	if len(speakers) == 0 {
		builder.WriteString("set speaker to A for sentences in the first voice heard, B for the second voice and so on.")
		return builder.String()
	}
	builder.WriteString("the first attached audio is the recording to transcribe and each following attachment is a short recording of one person:")
	for index, speaker := range speakers {
		fmt.Fprintf(&builder, " attachment %d is %q;", index+2, speaker.name)
	}
	builder.WriteString(" set speaker to that person's name for sentences in their voice, and to A for the first other voice heard, B for the second and so on.")
	return builder.String()
}

// parseDiarizedTranscript 按 JSON Schema 解析模型返回的带说话人分段转写
func parseDiarizedTranscript(bridge ToolBridge, text string) (diarizedTranscript, error) {
	var transcript diarizedTranscript
	parsed, _, err := bridge.Parse(text)
	if err == nil {
		err = json.Unmarshal([]byte(parsed), &transcript)
	}
	return transcript, err
}

// streamDiarizedTranscription 以 transcript.text.segment 与 transcript.text.done 事件流式输出带说话人的转写
func streamDiarizedTranscription(c *gin.Context, pool *balancer.AccountPool, request transcriptionRequest, id string, bridge ToolBridge, prepare func(*gemini.Client, bool) (string, []gemini.FileData, error)) {
	setSSEHeaders(c)
	defer startKeepalive(c, openAIKeepalive)()
	result, accountID, err := runGeneration(c.Request.Context(), pool, "", sessionCreate, request.model, id, generationMedia{}, gemini.ThinkingLow, bridge, prepare, nil, nil)
	c.Set("account_id", accountID)
	var transcript diarizedTranscript
	if err == nil {
		if transcript, err = parseDiarizedTranscript(bridge, strings.TrimSpace(result.Accumulator.Primary().Text)); err != nil {
			_ = writeOpenAIStreamError(c.Writer, http.StatusBadGateway, "output_validation_error", err)
			return
		}
	}
	if err != nil {
		_ = writeOpenAIStreamError(c.Writer, upstreamStatus(err), openAIUpstreamCode(err), streamFailure(c, err))
		return
	}
	for _, segment := range diarizedSegments(transcript.Segments) {
		if err := writeSSEJSON(c.Writer, segment); err != nil {
			return
		}
	}
	_ = writeSSEJSON(c.Writer, gin.H{"type": "transcript.text.done", "text": transcript.Text, "usage": transcriptionUsage(result.Accumulator.Usage, request.file)})
}

// diarizedTranscription 返回 OpenAI diarized_json 形状的转写结果
func diarizedTranscription(request transcriptionRequest, transcript diarizedTranscript, usage gin.H) gin.H {
	duration := request.audioDuration
	for _, segment := range transcript.Segments {
		duration = math.Max(duration, segment.End)
	}
	return gin.H{"task": "transcribe", "duration": duration, "text": transcript.Text, "segments": diarizedSegments(transcript.Segments), "usage": usage}
}

// diarizedSegments 把模型返回的分段转为 transcript.text.segment 项
func diarizedSegments(segments []diarizedSegment) []gin.H {
	items := make([]gin.H, 0, len(segments))
	for index, segment := range segments {
		items = append(items, gin.H{
			"type": "transcript.text.segment", "id": fmt.Sprintf("seg_%03d", index), "speaker": segment.Speaker,
			"start": segment.Start, "end": segment.End, "text": segment.Text,
		})
	}
	return items
}
