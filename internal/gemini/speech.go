package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
)

// speechRPC 是官网回答菜单 Listen 朗读使用的 batchexecute RPC
const speechRPC = "XqA3Ic"

// SpeechFormat 是朗读音频的容器格式，取值写入 XqA3Ic 请求第 4 槽
type SpeechFormat int

const (
	// SpeechMP3 是 24 kHz 单声道 MPEG-2 Layer III
	SpeechMP3 SpeechFormat = 1
	// SpeechOggOpus 是官网页面播放使用的 Ogg Opus
	SpeechOggOpus SpeechFormat = 2
)

// SpeechTextLimit 是单次朗读接受的最大字符数
const SpeechTextLimit = 4096

// Speech 以官网朗读 RPC 把文本合成为音频，language 为空时使用账号界面语言；调用方持有账号请求槽
func (c *Client) Speech(ctx context.Context, text string, language string, format SpeechFormat) ([]byte, error) {
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return nil, err
	}
	if language == "" {
		language = bootstrap.Language
	}
	payload, err := pageJSON([]any{nil, text, language, nil, int(format)})
	if err != nil {
		return nil, err
	}
	var mode int
	if model, err := bootstrap.Catalog.Default(); err == nil {
		mode = model.Mode
	}
	result, err := c.batchExecute(ctx, bootstrap, batchRequest{
		rpcID: speechRPC, payload: payload, sourcePath: "/app", language: bootstrap.Language, extension: "[0]", mode: mode, level: ThinkingLow,
	})
	if err != nil {
		return nil, err
	}
	encoded, _ := stringAt(result, 0)
	audio, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(audio) < 16 {
		return nil, retryableProtocolError(fmt.Sprintf("%s returned no audio", speechRPC), err)
	}
	return audio, nil
}
