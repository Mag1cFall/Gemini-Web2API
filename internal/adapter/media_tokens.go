package adapter

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
)

// Gemini API 官方计数规则：两边都不超过 384 像素的图片计 258，较大的图片按 768×768 分块每块 258；音频每秒 32；视频每秒 263；PDF 每页按一张图片计
const (
	imageTileTokens     = 258
	imageSmallEdge      = 384
	imageTileEdge       = 768
	audioTokensPerSec   = 32
	videoTokensPerSec   = 263
	unknownAudioBitRate = 128000
	unknownVideoBitRate = 1000000
)

// mediaModality 是 Gemini promptTokensDetails 的模态名
type mediaModality string

const (
	modalityText     mediaModality = "TEXT"
	modalityImage    mediaModality = "IMAGE"
	modalityAudio    mediaModality = "AUDIO"
	modalityVideo    mediaModality = "VIDEO"
	modalityDocument mediaModality = "DOCUMENT"
)

// mediaTokenCount 按模态汇总附件的估算 token 数
func mediaTokenCount(files []gemini.FileData) map[mediaModality]int {
	counts := make(map[mediaModality]int)
	for _, file := range files {
		modality, tokens := fileTokens(file)
		counts[modality] += tokens
	}
	return counts
}

// mediaTokens 返回附件按官方计数规则估算的 token 总数
func mediaTokens(files []gemini.FileData) int {
	total := 0
	for _, tokens := range mediaTokenCount(files) {
		total += tokens
	}
	return total
}

// fileTokens 返回单个附件的模态与估算 token 数
func fileTokens(file gemini.FileData) (mediaModality, int) {
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(file.MimeType, ";")[0]))
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = strings.ToLower(strings.Split(http.DetectContentType(file.Data), ";")[0])
	}
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return modalityImage, imageTokens(file.Data)
	case strings.HasPrefix(mimeType, "audio/"):
		return modalityAudio, int(math.Ceil(audioSeconds(file.Data) * audioTokensPerSec))
	case strings.HasPrefix(mimeType, "video/"):
		return modalityVideo, int(math.Ceil(videoSeconds(file.Data) * videoTokensPerSec))
	case mimeType == "application/pdf":
		return modalityDocument, pdfPages(file.Data) * imageTileTokens
	case strings.HasPrefix(mimeType, "text/") || utf8.Valid(file.Data):
		return modalityText, tokencount.Text(string(file.Data))
	}
	return modalityDocument, imageTileTokens
}

// imageTokens 按图片尺寸计算 token，尺寸无法识别时按一块计
func imageTokens(data []byte) int {
	width, height := imageSize(data)
	if width <= 0 || height <= 0 || width <= imageSmallEdge && height <= imageSmallEdge {
		return imageTileTokens
	}
	tiles := int(math.Ceil(float64(width)/imageTileEdge) * math.Ceil(float64(height)/imageTileEdge))
	return tiles * imageTileTokens
}

// imageSize 返回 PNG、JPEG、GIF 或 WebP 图片的宽高
func imageSize(data []byte) (int, int) {
	if config, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return config.Width, config.Height
	}
	if len(data) < 30 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0
	}
	switch string(data[12:16]) {
	case "VP8 ":
		return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
	case "VP8L":
		bits := binary.LittleEndian.Uint32(data[21:25])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1
	case "VP8X":
		width := int(data[24]) | int(data[25])<<8 | int(data[26])<<16
		height := int(data[27]) | int(data[28])<<8 | int(data[29])<<16
		return width + 1, height + 1
	}
	return 0, 0
}

// audioSeconds 返回音频时长：WAV 与 FLAC 读取头部，Ogg Opus 读取末页位置，MP3 按首帧码率，其余按 128 kbps 估算
func audioSeconds(data []byte) float64 {
	if seconds, ok := wavSeconds(data); ok {
		return seconds
	}
	if seconds, ok := opusSeconds(data); ok {
		return seconds
	}
	if len(data) >= 26 && string(data[:4]) == "fLaC" {
		info := data[8:]
		rate := int(info[10])<<12 | int(info[11])<<4 | int(info[12])>>4
		samples := uint64(info[13]&0x0f)<<32 | uint64(binary.BigEndian.Uint32(info[14:18]))
		if rate > 0 && samples > 0 {
			return float64(samples) / float64(rate)
		}
	}
	if bitRate := mp3BitRate(data); bitRate > 0 {
		return float64(len(data)) * 8 / float64(bitRate)
	}
	return float64(len(data)) * 8 / unknownAudioBitRate
}

// wavSeconds 返回 RIFF WAVE 的 data 块时长
func wavSeconds(data []byte) (float64, bool) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, false
	}
	byteRate := 0
	for offset := 12; offset+8 <= len(data); {
		kind := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if kind == "fmt " && body+12 <= len(data) {
			byteRate = int(binary.LittleEndian.Uint32(data[body+8 : body+12]))
		}
		if kind == "data" && byteRate > 0 {
			return float64(min(size, len(data)-body)) / float64(byteRate), true
		}
		offset = body + size + size%2
	}
	return 0, false
}

// opusSeconds 返回 Ogg Opus 末页 granule position 减去 pre-skip 后按 48 kHz 计的时长
func opusSeconds(data []byte) (float64, bool) {
	if len(data) < 47 || string(data[:4]) != "OggS" || string(data[28:36]) != "OpusHead" {
		return 0, false
	}
	last := bytes.LastIndex(data, []byte("OggS"))
	if last+14 > len(data) {
		return 0, false
	}
	granule := int64(binary.LittleEndian.Uint64(data[last+6 : last+14]))
	samples := granule - int64(binary.LittleEndian.Uint16(data[38:40]))
	return float64(samples) / 48000, samples > 0
}

// mp3BitRate 返回首个 MPEG 音频帧的码率，跳过 ID3v2 标签
func mp3BitRate(data []byte) int {
	offset := 0
	if len(data) >= 10 && string(data[:3]) == "ID3" {
		offset = 10 + int(data[6]&0x7f)<<21 | int(data[7]&0x7f)<<14 | int(data[8]&0x7f)<<7 | int(data[9]&0x7f)
	}
	mpeg1 := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	mpeg2 := []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	for ; offset+4 <= len(data) && offset < 1<<16; offset++ {
		if data[offset] != 0xff || data[offset+1]&0xe0 != 0xe0 || data[offset+1]&0x06 != 0x02 {
			continue
		}
		index := int(data[offset+2] >> 4)
		if index == 0 || index == 15 {
			continue
		}
		if data[offset+1]&0x18 == 0x18 {
			return mpeg1[index] * 1000
		}
		return mpeg2[index] * 1000
	}
	return 0
}

// videoSeconds 返回 MP4 与 QuickTime 的 mvhd 时长，其余按 1 Mbps 估算
func videoSeconds(data []byte) float64 {
	if index := bytes.Index(data, []byte("mvhd")); index >= 4 && index+24 <= len(data) {
		box := data[index+4:]
		if box[0] == 1 && len(box) >= 32 {
			scale := binary.BigEndian.Uint32(box[20:24])
			duration := binary.BigEndian.Uint64(box[24:32])
			if scale > 0 {
				return float64(duration) / float64(scale)
			}
		}
		if box[0] == 0 && len(box) >= 20 {
			scale := binary.BigEndian.Uint32(box[12:16])
			duration := binary.BigEndian.Uint32(box[16:20])
			if scale > 0 {
				return float64(duration) / float64(scale)
			}
		}
	}
	return float64(len(data)) * 8 / unknownVideoBitRate
}

// pdfPages 统计 PDF 页对象数，至少为 1
func pdfPages(data []byte) int {
	pages := bytes.Count(data, []byte("/Type /Page")) - bytes.Count(data, []byte("/Type /Pages"))
	pages += bytes.Count(data, []byte("/Type/Page")) - bytes.Count(data, []byte("/Type/Pages"))
	return max(pages, 1)
}
