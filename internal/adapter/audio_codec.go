package adapter

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/hajimehoshi/go-mp3"
	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	aacpcm "github.com/tphakala/go-aac/pcm"
)

// audioPCM 是 16 位小端交错 PCM 及其采样率与声道数
type audioPCM struct {
	Data       []byte
	SampleRate int
	Channels   int
}

// audioContentTypes 是各输出格式的 Content-Type
var audioContentTypes = map[string]string{
	"mp3": "audio/mpeg", "opus": "audio/ogg", "wav": "audio/wav", "pcm": "audio/pcm", "pcm16": "audio/pcm", "flac": "audio/flac", "aac": "audio/aac",
}

// transcodeMP3 把 MP3 转为 OpenAI 音频格式，返回数据与 Content-Type；pcm 为 24 kHz 单声道
func transcodeMP3(data []byte, format string) ([]byte, string, error) {
	if format == "" || format == "mp3" {
		return data, audioContentTypes["mp3"], nil
	}
	contentType, ok := audioContentTypes[format]
	if !ok {
		return nil, "", fmt.Errorf("audio format %q is not available: Gemini Web returns MP3 audio and this service converts it to mp3, opus, aac, wav, flac or pcm", format)
	}
	audio, err := decodeMP3(data)
	if err != nil {
		return nil, "", err
	}
	switch format {
	case "wav":
		return pcmWAV(audio), contentType, nil
	case "pcm", "pcm16":
		return resamplePCM(mixMono(audio), 24000).Data, contentType, nil
	case "flac":
		encoded, err := encodeFLAC(audio)
		return encoded, contentType, err
	case "aac":
		encoded, err := encodeAAC(audio)
		return encoded, contentType, err
	default:
		encoded, err := encodeOggOpus(resamplePCM(audio, 48000))
		return encoded, contentType, err
	}
}

// decodeMP3 把 MP3 解码为 PCM，左右声道完全相同时合并为单声道
func decodeMP3(data []byte) (audioPCM, error) {
	decoder, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return audioPCM{}, fmt.Errorf("decode MP3 audio: %w", err)
	}
	pcm, err := io.ReadAll(decoder)
	if err != nil {
		return audioPCM{}, fmt.Errorf("decode MP3 audio: %w", err)
	}
	audio := audioPCM{Data: pcm, SampleRate: decoder.SampleRate(), Channels: 2}
	for offset := 0; offset+4 <= len(pcm); offset += 4 {
		if pcm[offset] != pcm[offset+2] || pcm[offset+1] != pcm[offset+3] {
			return audio, nil
		}
	}
	return mixMono(audio), nil
}

// mixMono 把多声道 PCM 平均为单声道
func mixMono(audio audioPCM) audioPCM {
	if audio.Channels == 1 {
		return audio
	}
	frames := len(audio.Data) / 2 / audio.Channels
	data := make([]byte, frames*2)
	for index := range frames {
		sum := 0
		for channel := range audio.Channels {
			sum += int(int16(binary.LittleEndian.Uint16(audio.Data[(index*audio.Channels+channel)*2:])))
		}
		binary.LittleEndian.PutUint16(data[index*2:], uint16(int16(sum/audio.Channels)))
	}
	return audioPCM{Data: data, SampleRate: audio.SampleRate, Channels: 1}
}

// resamplePCM 以线性插值把 PCM 转到目标采样率
func resamplePCM(audio audioPCM, rate int) audioPCM {
	if audio.SampleRate == rate || audio.SampleRate <= 0 {
		return audio
	}
	frames := len(audio.Data) / 2 / audio.Channels
	outFrames := int(int64(frames) * int64(rate) / int64(audio.SampleRate))
	data := make([]byte, outFrames*audio.Channels*2)
	sample := func(frame int, channel int) float64 {
		frame = min(frame, frames-1)
		return float64(int16(binary.LittleEndian.Uint16(audio.Data[(frame*audio.Channels+channel)*2:])))
	}
	for index := range outFrames {
		position := float64(index) * float64(audio.SampleRate) / float64(rate)
		left := int(position)
		fraction := position - float64(left)
		for channel := range audio.Channels {
			value := sample(left, channel)*(1-fraction) + sample(left+1, channel)*fraction
			binary.LittleEndian.PutUint16(data[(index*audio.Channels+channel)*2:], uint16(int16(value)))
		}
	}
	return audioPCM{Data: data, SampleRate: rate, Channels: audio.Channels}
}

// pcmWAV 把 PCM 封装为 RIFF WAVE
func pcmWAV(audio audioPCM) []byte {
	buffer := bytes.NewBuffer(make([]byte, 0, 44+len(audio.Data)))
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+len(audio.Data)))
	buffer.WriteString("WAVEfmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(audio.Channels))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(audio.SampleRate))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(audio.SampleRate*audio.Channels*2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(audio.Channels*2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(16))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(len(audio.Data)))
	buffer.Write(audio.Data)
	return buffer.Bytes()
}

// encodeFLAC 以 4096 样本定长块编码 16 位 FLAC
func encodeFLAC(audio audioPCM) ([]byte, error) {
	const blockSize = 4096
	frames := len(audio.Data) / 2 / audio.Channels
	var output bytes.Buffer
	encoder, err := flac.NewEncoder(&output, &meta.StreamInfo{
		BlockSizeMin: blockSize, BlockSizeMax: blockSize, SampleRate: uint32(audio.SampleRate),
		NChannels: uint8(audio.Channels), BitsPerSample: 16, NSamples: uint64(frames), MD5sum: md5.Sum(audio.Data),
	})
	if err != nil {
		return nil, err
	}
	for start := 0; start < frames; start += blockSize {
		count := min(blockSize, frames-start)
		subframes := make([]*frame.Subframe, audio.Channels)
		for channel := range subframes {
			samples := make([]int32, count)
			for index := range samples {
				samples[index] = int32(int16(binary.LittleEndian.Uint16(audio.Data[((start+index)*audio.Channels+channel)*2:])))
			}
			subframes[channel] = &frame.Subframe{SubHeader: frame.SubHeader{Pred: frame.PredVerbatim}, NSamples: count, Samples: samples}
		}
		if err := encoder.WriteFrame(&frame.Frame{Header: frame.Header{
			HasFixedBlockSize: true, BlockSize: uint16(count), SampleRate: uint32(audio.SampleRate),
			Channels: frame.Channels(audio.Channels - 1), BitsPerSample: 16,
		}, Subframes: subframes}); err != nil {
			return nil, err
		}
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// encodeAAC 把 PCM 重采样到 44.1 kHz 后编码为 ADTS 封装的 AAC-LC
func encodeAAC(audio audioPCM) ([]byte, error) {
	audio = resamplePCM(audio, 44100)
	var output bytes.Buffer
	err := aacpcm.EncodeInterleaved(&output, aacpcm.Config{SampleRate: audio.SampleRate, BitDepth: 16, Channels: audio.Channels}, audio.Data)
	return output.Bytes(), err
}

// encodeOggOpus 以 20 ms 帧把 48 kHz PCM 编码为 Ogg Opus
func encodeOggOpus(audio audioPCM) ([]byte, error) {
	encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: audio.SampleRate, Channels: audio.Channels, Application: gopus.ApplicationAudio})
	if err != nil {
		return nil, err
	}
	preSkip := encoder.Lookahead()
	var output bytes.Buffer
	writer, err := ogg.NewWriterWithConfig(&output, ogg.WriterConfig{
		SampleRate: uint32(audio.SampleRate), Channels: uint8(audio.Channels), PreSkip: uint16(preSkip), StreamCount: 1, CoupledCount: uint8(audio.Channels - 1),
	})
	if err != nil {
		return nil, err
	}
	samples := make([]int16, len(audio.Data)/2)
	_ = binary.Read(bytes.NewReader(audio.Data), binary.LittleEndian, samples)
	frameSize := audio.SampleRate / 50
	end := preSkip + len(samples)/audio.Channels
	chunk := make([]int16, frameSize*audio.Channels)
	packet := make([]byte, 4000)
	for start, granule := 0, 0; granule < end; start += len(chunk) {
		clear(chunk)
		if start < len(samples) {
			copy(chunk, samples[start:])
		}
		count, err := encoder.EncodeInt16(chunk, packet)
		if err != nil {
			return nil, err
		}
		duration := min(frameSize, end-granule)
		if err := writer.WritePacket(packet[:count], duration); err != nil {
			return nil, err
		}
		granule += duration
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
