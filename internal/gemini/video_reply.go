package gemini

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrVideoQuota 表示账号的视频生成额度已用尽或视频生成暂时受限
var ErrVideoQuota = errors.New("账号的视频生成额度已用尽或暂时受限")

// VideoQuotaError 携带上游在视频额度拒答中给出的重置时刻，ResetAt 为零时使用默认模型冷却时长
type VideoQuotaError struct {
	ResetAt time.Time
}

// Error 返回视频额度受限的说明
func (e *VideoQuotaError) Error() string {
	return ErrVideoQuota.Error()
}

// Is 把 VideoQuotaError 匹配到 ErrVideoQuota
func (e *VideoQuotaError) Is(target error) bool {
	return target == ErrVideoQuota
}

// VideoQuotaReset 返回错误携带的视频额度重置时刻，未携带时为零值
func VideoQuotaReset(err error) time.Time {
	var quotaErr *VideoQuotaError
	if errors.As(err, &quotaErr) {
		return quotaErr.ResetAt
	}
	return time.Time{}
}

// videoQuotaPhrases 是官网在视频额度用尽或视频限流时以文字回复给出的特征短语（小写匹配）
var videoQuotaPhrases = []string{
	"out of video generation quota",
	"limit resets",
	"reached your limit",
	"video limit",
	"usage limit",
	"a lot of requests",
	"too many requests",
	"try again later",
	"try again in",
}

// videoRefusalPhrases 是官网拒绝生成视频但未给出固定失败文字时的拒答短语（小写匹配）
var videoRefusalPhrases = []string{
	"can't generate",
	"cannot generate",
	"can't create",
	"unable to generate",
	"can't help with",
}

// maxVideoResetHorizon 是接受上游重置时刻的最大跨度
const maxVideoResetHorizon = 7*24*time.Hour + time.Hour

var (
	videoResetRelativePattern = regexp.MustCompile(`(?i)\bin\s+(\d+)\s*(seconds?|secs?|minutes?|mins?|hours?|hrs?)\b`)
	videoResetMonthDayPattern = regexp.MustCompile(`(?i)\b(?:on\s+)?(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\s+(\d{1,2})(?:,\s*|\s+at\s+)(\d{1,2}:\d{2}\s*(?:AM|PM))\b`)
	videoResetClockPattern    = regexp.MustCompile(`(?i)\b(?:at|until)\s+(\d{1,2}:\d{2}\s*(?:AM|PM))\b`)
)

// isVideoQuotaText 判断视频请求的文字回复是否属于额度用尽或限流拒答
func isVideoQuotaText(text string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range videoQuotaPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// isVideoRefusalText 判断视频请求的文字回复是否属于视频拒答
func isVideoRefusalText(text string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range videoRefusalPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// parseVideoResetTime 从上游额度提示或拒答正文中解析未来重置时刻，未找到或超出合理范围时返回零值
func parseVideoResetTime(now time.Time, texts ...string) time.Time {
	for _, text := range texts {
		if text == "" {
			continue
		}
		if match := videoResetRelativePattern.FindStringSubmatch(text); match != nil {
			amount, err := strconv.Atoi(match[1])
			if err == nil && amount > 0 {
				var unit time.Duration
				switch strings.ToLower(match[2][:1]) {
				case "s":
					unit = time.Second
				case "m":
					unit = time.Minute
				case "h":
					unit = time.Hour
				}
				if delay := time.Duration(amount) * unit; delay > 0 && delay <= maxVideoResetHorizon {
					return now.Add(delay).Round(0)
				}
			}
		}
		if match := videoResetMonthDayPattern.FindStringSubmatch(text); match != nil {
			month := strings.ToUpper(match[1][:1]) + strings.ToLower(match[1][1:3])
			stamp := fmt.Sprintf("%d %s %s %s", now.Year(), month, match[2], normalizeMeridiemClock(match[3]))
			if parsed, err := time.ParseInLocation("2006 Jan 2 3:04 PM", stamp, now.Location()); err == nil {
				if !parsed.After(now) && now.Month() == time.December && parsed.Month() == time.January {
					parsed = parsed.AddDate(1, 0, 0)
				}
				if validVideoReset(now, parsed) {
					return parsed
				}
			}
		}
		if match := videoResetClockPattern.FindStringSubmatch(text); match != nil {
			stamp := fmt.Sprintf("%s %s", now.Format("2006-01-02"), normalizeMeridiemClock(match[1]))
			if parsed, err := time.ParseInLocation("2006-01-02 3:04 PM", stamp, now.Location()); err == nil {
				if !parsed.After(now) {
					parsed = parsed.AddDate(0, 0, 1)
				}
				if validVideoReset(now, parsed) {
					return parsed
				}
			}
		}
	}
	return time.Time{}
}

// normalizeMeridiemClock 把 12 小时制时间字符串规范为 "3:04 PM" 形式
func normalizeMeridiemClock(raw string) string {
	cleaned := strings.ToUpper(strings.TrimSpace(raw))
	suffix := cleaned[len(cleaned)-2:]
	clock := strings.TrimSpace(cleaned[:len(cleaned)-2])
	return clock + " " + suffix
}

// validVideoReset 判断候选重置时刻是否位于未来合理窗口内
func validVideoReset(now, reset time.Time) bool {
	return reset.After(now) && !reset.After(now.Add(maxVideoResetHorizon))
}

// videoQuotaErrorFromRichContent 从视频富内容字段中提取额度用尽状态与重置时间戳，未命中或包含成品视频地址时返回 nil
func videoQuotaErrorFromRichContent(raw any) *ProtocolError {
	file, _ := valuePath(raw, 0, 0, 0, 0)
	if mediaURL, _ := stringPath(file, 7, 1); mediaURL != "" {
		return nil
	}
	detail, resetUnix := scanVideoQuotaRichContent(raw)
	if detail == "" {
		return nil
	}
	now := time.Now()
	var resetAt time.Time
	if resetUnix > 0 {
		if candidate := time.Unix(resetUnix, 0); validVideoReset(now, candidate) {
			resetAt = candidate
		}
	}
	return videoQuotaError("", detail, resetAt)
}

// scanVideoQuotaRichContent 递归扫描视频富内容中的额度耗尽说明与 [unix秒, 纳秒] 重置时间戳
func scanVideoQuotaRichContent(value any) (string, int64) {
	var detail string
	var resetUnix int64
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case string:
			if detail == "" && strings.Contains(strings.ToLower(typed), "out of video generation quota") {
				detail = typed
			}
		case []any:
			if len(typed) == 2 {
				sec, okSec := int64Value(typed[0])
				ns, okNS := int64Value(typed[1])
				if okSec && okNS && sec >= 1_700_000_000 && sec < 2_000_000_000 && ns >= 0 && ns < 1_000_000_000 && sec > resetUnix {
					resetUnix = sec
				}
			}
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(value)
	return detail, resetUnix
}

// int64Value 把 JSON 数字解码出的数值转为 int64
func int64Value(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), float64(int64(typed)) == typed
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	}
	return 0, false
}

// videoQuotaError 返回视频额度用尽或视频限流的可重试 429 协议错误
func videoQuotaError(text, limitNotice string, resetAt time.Time) *ProtocolError {
	detail := strings.TrimSpace(limitNotice)
	if detail == "" {
		detail = strings.TrimSpace(text)
	}
	message := "账号的视频生成额度已用尽或暂时受限，稍后重试或切换其他账号"
	if !resetAt.IsZero() {
		message = fmt.Sprintf("账号的视频生成额度已用尽或暂时受限，预计 %s 后恢复", resetAt.Local().Format(time.TimeOnly))
	}
	if detail != "" {
		message += "：" + detail
	}
	return &ProtocolError{
		HTTPStatus: 429,
		Code:       429,
		Message:    message,
		Retryable:  true,
		Cause:      &VideoQuotaError{ResetAt: resetAt},
	}
}

// videoRefusalError 返回官网拒绝生成视频的可重试 502 协议错误
func videoRefusalError(text string) *ProtocolError {
	return &ProtocolError{
		HTTPStatus: 502,
		Code:       502,
		Message:    fmt.Sprintf("官网拒绝生成视频，未返回视频文件：%s", strings.TrimSpace(text)),
		Retryable:  true,
		Cause:      ErrUpstreamFailureText,
	}
}

// noVideoError 返回视频生成只有文字回答或没有内容、未产出视频时的可重试 502 协议错误
func noVideoError(text string) *ProtocolError {
	message := "官网未返回生成的视频，请稍后重试"
	if text = strings.TrimSpace(text); text != "" {
		message = "官网未生成视频，返回了文字回复：" + text
	}
	return &ProtocolError{HTTPStatus: 502, Code: 502, Message: message, Retryable: true, Cause: ErrUpstreamFailureText}
}
