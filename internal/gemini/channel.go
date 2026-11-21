package gemini

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// channelURL 是官网生成使用的 processSession WebChannel 地址
const channelURL = "https://geminiweb-pa.clients6.google.com/v1/processSession"

// channelHeaderOrder 是 Chrome 稳定版页面 processSession 请求的请求头顺序
var channelHeaderOrder = []string{
	"content-length", "sec-ch-ua-full-version-list", "sec-ch-ua-platform", "sec-ch-ua",
	"sec-ch-ua-bitness", "sec-ch-ua-model", "sec-ch-ua-mobile", "sec-ch-ua-form-factors",
	"sec-ch-ua-wow64", "sec-ch-ua-arch", "sec-ch-ua-full-version", "user-agent", "content-type",
	"sec-ch-ua-platform-version", "accept", "origin",
	"x-browser-channel", "x-browser-year", "x-browser-validation", "x-browser-copyright", "x-client-data", "x-sec-ch-ua-not-a-border",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer",
	"accept-encoding", "accept-language", "cookie", "priority",
}

// channelSessionPattern 匹配握手响应中的会话 SID
var channelSessionPattern = regexp.MustCompile(`\["c","([^"]+)"`)

// channelEndMessage 是官网在回答结束或停止回答时发送的结束消息
var channelEndMessage = []any{nil, nil, nil, []any{1}}

// channelCommitted 是 payload[25] 中表示回答已提交的状态值，Task 等回答在它之后才给出完成的候选与收尾帧
const channelCommitted = "response_committed"

// channel 是一次生成使用的 processSession 会话
type channel struct {
	client     *Client
	gsessionid string
	sid        string

	mu sync.Mutex
	// rid 是下一个上行请求的序号，ofs 是已发送的消息数，aid 是已收到的最后一个下行数组 ID
	rid   int
	ofs   int
	aid   int
	ended bool
	// responded 表示已取得第一个长轮询的响应头
	responded bool
}

// streamRecord 是生成流中的一条记录：生成载荷、Bard 错误码、上游状态错误或本轮收尾
type streamRecord struct {
	payload  []any
	bardCode int
	err      *ProtocolError
	end      bool
}

// channelHeaders 返回握手时与会话绑定的 $httpHeaders：登录会话带 SAPISIDHASH 授权与账号序号，扩展头写成 base64 protobuf，消息按 JSPB 编码
func (c *Client) channelHeaders(bootstrap Bootstrap, modelHeader []byte, requestID string) string {
	var lines []string
	if !c.guest {
		lines = append(lines, "Authorization:"+c.sapisidHash(time.Now()), "X-Goog-AuthUser:"+strconv.Itoa(c.authUser))
	}
	encode := base64.StdEncoding.EncodeToString
	lines = append(lines,
		"X-Goog-Api-Key:"+bootstrap.APIKey,
		"X-Server-Token:"+bootstrap.ServerToken,
		"x-goog-ext-525001261-bin:"+encode(modelHeader),
		"x-goog-ext-73010989-bin:"+encode(encodeJSPB([]any{0})),
		"x-goog-ext-525005358-bin:"+encode(encodeJSPB([]any{requestID, 1})),
		"x-goog-ext-73010990-bin:"+encode(encodeJSPB([]any{0, 0, 0})),
		"X-WebChannel-Content-Type:application/json+protobuf",
	)
	return strings.Join(lines, "\r\n") + "\r\n"
}

// sapisidHash 按官网页面以 SAPISID、__Secure-1PAPISID 与 __Secure-3PAPISID 计算 Authorization 值
func (c *Client) sapisidHash(now time.Time) string {
	stamp := strconv.FormatInt(now.Unix(), 10)
	hash := func(name string) string {
		c.cookieMu.Lock()
		value := c.cookies[cookieKey(Cookie{Domain: ".google.com", Path: "/", Name: name})].Value
		c.cookieMu.Unlock()
		sum := sha1.Sum([]byte(stamp + " " + value + " " + geminiOrigin))
		return stamp + "_" + hex.EncodeToString(sum[:])
	}
	return "SAPISIDHASH " + hash("SAPISID") + " SAPISID1PHASH " + hash("__Secure-1PAPISID") + " SAPISID3PHASH " + hash("__Secure-3PAPISID")
}

// encodeJSPB 把 JSPB 数组按下标 n 对应字段 n+1 编码为 protobuf：null 省略，整数写 varint，字符串写长度字段，整数切片写 packed 字段，其余数组写嵌套消息
func encodeJSPB(values []any) []byte {
	var out []byte
	for index, value := range values {
		number := protowire.Number(index + 1)
		switch item := value.(type) {
		case nil:
		case int:
			out = protowire.AppendVarint(protowire.AppendTag(out, number, protowire.VarintType), uint64(item))
		case int64:
			out = protowire.AppendVarint(protowire.AppendTag(out, number, protowire.VarintType), uint64(item))
		case string:
			out = protowire.AppendString(protowire.AppendTag(out, number, protowire.BytesType), item)
		case []int:
			var packed []byte
			for _, element := range item {
				packed = protowire.AppendVarint(packed, uint64(element))
			}
			out = protowire.AppendBytes(protowire.AppendTag(out, number, protowire.BytesType), packed)
		case []string:
			nested := make([]any, len(item))
			for position, element := range item {
				nested[position] = element
			}
			out = protowire.AppendBytes(protowire.AppendTag(out, number, protowire.BytesType), encodeJSPB(nested))
		case []any:
			out = protowire.AppendBytes(protowire.AppendTag(out, number, protowire.BytesType), encodeJSPB(item))
		default:
			panic(fmt.Sprintf("encodeJSPB: unsupported %T at field %d", value, number))
		}
	}
	return out
}

// channelZX 按页面脚本 goog.string.getRandomString 生成 zx 参数：两个小于 2^31 的随机数转 36 进制后相连，第二个先与当前毫秒时间按 32 位异或再取绝对值
func channelZX() string {
	const limit = 1 << 31
	mixed := int64(int32(rand.Int64N(limit)) ^ int32(time.Now().UnixMilli()))
	if mixed < 0 {
		mixed = -mixed
	}
	return strconv.FormatInt(rand.Int64N(limit), 36) + strconv.FormatInt(mixed, 36)
}

// channelEscape 按页面 encodeURIComponent 编码查询参数与表单值
func channelEscape(value string) string {
	return formEscaper.Replace(url.QueryEscape(value))
}

// channelRequest 以 Chrome 页面 processSession 请求的请求头发出请求并吸收响应 Cookie，beacon 为页面 sendBeacon 发出的 terminate
func (c *Client) channelRequest(ctx context.Context, method, query, body string, beacon bool) (*http.Response, error) {
	var reader io.Reader
	if method == http.MethodPost {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, channelURL+"?"+query, reader)
	if err != nil {
		return nil, err
	}
	c.applyChromeHeaders(req, true)
	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
	req.Header.Set("origin", geminiOrigin)
	req.Header.Set("referer", geminiOrigin+"/")
	req.Header.Set("sec-fetch-site", "same-site")
	req.Header.Set("sec-fetch-dest", "empty")
	switch {
	case beacon:
		req.Header.Set("content-type", "text/plain;charset=UTF-8")
		req.Header.Set("sec-fetch-mode", "no-cors")
		req.Header.Set("priority", "u=4, i")
	case method == http.MethodPost:
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
		fallthrough
	default:
		req.Header.Set("sec-fetch-mode", "cors")
		req.Header.Set("priority", "u=1, i")
	}
	req.Header[http.HeaderOrderKey] = channelHeaderOrder
	req.Header[http.PHeaderOrderKey] = pseudoHeaderOrder
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportProtocolError("连接生成会话", err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

// openChannel 以握手请求建立 processSession 会话，headers 为与会话绑定的 $httpHeaders
func (c *Client) openChannel(ctx context.Context, headers string) (*channel, error) {
	rid := rand.IntN(100000)
	query := "VER=8&RID=" + strconv.Itoa(rid) + "&CVER=22&X-HTTP-Session-Id=gsessionid&%24httpHeaders=" + channelEscape(headers) + "&zx=" + channelZX() + "&t=1"
	resp, err := c.channelRequest(ctx, http.MethodPost, query, "count=0", false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseStatusError(resp, "建立生成会话")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, transportProtocolError("建立生成会话", err)
	}
	match := channelSessionPattern.FindSubmatch(body)
	gsessionid := resp.Header.Get("X-HTTP-Session-Id")
	if match == nil || gsessionid == "" {
		return nil, retryableProtocolError(fmt.Sprintf("建立生成会话时官网没有返回会话: %s", body), nil)
	}
	return &channel{client: c, gsessionid: gsessionid, sid: string(match[1]), rid: rid + 1}, nil
}

// send 在一个上行请求中依次发送 JSPB 编码的 messages
func (ch *channel) send(ctx context.Context, messages ...any) error {
	ch.mu.Lock()
	var body strings.Builder
	body.WriteString("count=" + strconv.Itoa(len(messages)) + "&ofs=" + strconv.Itoa(ch.ofs))
	for index, message := range messages {
		encoded, err := pageJSON(message)
		if err != nil {
			ch.mu.Unlock()
			return fmt.Errorf("encode processSession message: %w", err)
		}
		body.WriteString("&req" + strconv.Itoa(index) + "___data__=" + channelEscape(encoded))
	}
	query := "VER=8&gsessionid=" + channelEscape(ch.gsessionid) + "&SID=" + channelEscape(ch.sid) + "&RID=" + strconv.Itoa(ch.rid) + "&AID=" + strconv.Itoa(ch.aid) + "&zx=" + channelZX() + "&t=1"
	ch.rid++
	ch.ofs += len(messages)
	ch.mu.Unlock()
	resp, err := ch.client.channelRequest(ctx, http.MethodPost, query, body.String(), false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseStatusError(resp, "发送生成请求")
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// end 发送一次结束消息，服务端随后发完剩余帧与收尾帧
func (ch *channel) end(ctx context.Context) error {
	ch.mu.Lock()
	ended := ch.ended
	ch.ended = true
	ch.mu.Unlock()
	if ended {
		return nil
	}
	return ch.send(ctx, channelEndMessage)
}

// close 在本轮结束后按官网页面的顺序发送结束消息并关闭会话
func (ch *channel) close() {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := ch.end(ctx); err != nil {
		logging.Warn(ch.client.accountID, "关闭生成会话失败", "原因", err)
	}
	ch.terminate()
}

// terminate 以页面 sendBeacon 的形式关闭会话
func (ch *channel) terminate() {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	ch.mu.Lock()
	query := "VER=8&gsessionid=" + channelEscape(ch.gsessionid) + "&SID=" + channelEscape(ch.sid) + "&RID=" + strconv.Itoa(ch.rid) + "&TYPE=terminate&zx=" + channelZX()
	ch.rid++
	ch.mu.Unlock()
	resp, err := ch.client.channelRequest(ctx, http.MethodPost, query, "", true)
	if err != nil {
		logging.Warn(ch.client.accountID, "关闭生成会话失败", "原因", err)
		return
	}
	resp.Body.Close()
}

// records 长轮询下行帧并依次交给 yield：生成载荷、错误与收尾；本轮在续接令牌帧、收尾帧或服务端关闭会话时结束
func (ch *channel) records(ctx context.Context, trace *logging.Request, yield func(streamRecord) error) error {
	for {
		ch.mu.Lock()
		query := "gsessionid=" + channelEscape(ch.gsessionid) + "&VER=8&RID=rpc&SID=" + channelEscape(ch.sid) + "&AID=" + strconv.Itoa(ch.aid) + "&CI=0&TYPE=xmlhttp&zx=" + channelZX() + "&t=1"
		ch.mu.Unlock()
		resp, err := ch.client.channelRequest(ctx, http.MethodGet, query, "", false)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return responseStatusError(resp, "接收回答")
		}
		if !ch.responded {
			ch.responded = true
			trace.UpstreamResponse(resp.StatusCode)
		}
		done, err := ch.readFrames(ctx, tracedReader{reader: resp.Body, trace: trace}, trace, yield)
		resp.Body.Close()
		if done || err != nil {
			return err
		}
	}
}

// readFrames 读取一次长轮询的全部帧，帧为“长度\nJSON”首尾相接，长度按 UTF-16 码元计，因此按 JSON 值边界切分；done 表示本轮已收尾或会话已关闭
func (ch *channel) readFrames(ctx context.Context, reader io.Reader, trace *logging.Request, yield func(streamRecord) error) (bool, error) {
	decoder := json.NewDecoder(reader)
	for {
		var length json.Number
		if err := decoder.Decode(&length); err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, transportProtocolError("接收回答", err)
		}
		var items [][]json.RawMessage
		if err := decoder.Decode(&items); err != nil {
			return false, transportProtocolError("接收回答", err)
		}
		for _, item := range items {
			id, _ := strconv.Atoi(string(item[0]))
			ch.mu.Lock()
			ch.aid = max(ch.aid, id)
			ch.mu.Unlock()
			done, err := ch.frame(ctx, item[1], trace, yield)
			if done || err != nil {
				return done, err
			}
		}
	}
}

// frame 处理一项下行内容：noop 为保活，close 为会话关闭，__sm__ 为上游状态，[null,null,null,[]] 为收尾帧，其余为生成消息；payload[25] 为续接令牌时本轮结束，为 response_committed 时发送结束消息并继续读到收尾帧
func (ch *channel) frame(ctx context.Context, raw json.RawMessage, trace *logging.Request, yield func(streamRecord) error) (bool, error) {
	var content any
	if err := json.Unmarshal(raw, &content); err != nil {
		return false, fmt.Errorf("解析官网回答失败: %w", err)
	}
	if envelope, ok := content.(map[string]any); ok {
		status, _ := envelope["__sm__"].(map[string]any)
		return false, yield(channelStatus(status["status"]))
	}
	values, _ := content.([]any)
	switch tag, _ := stringAt(values, 0); tag {
	case "noop":
		trace.UpstreamHeartbeat()
		return false, nil
	case "close":
		return true, nil
	}
	message, _ := arrayAt(values, 0)
	if len(message) == 4 && message[1] == nil {
		if _, ok := message[3].([]any); ok {
			return true, yield(streamRecord{end: true})
		}
	}
	items, _ := valuePath(message, 1, 6)
	list, _ := items.([]any)
	for _, item := range list {
		payload, ok := valuePath(item, 3, 3)
		root, _ := payload.([]any)
		if ok && root != nil {
			if err := yield(streamRecord{payload: root}); err != nil {
				return false, err
			}
		}
		switch token, ok := jspbString(root, 25); {
		case ok && token == channelCommitted:
			if err := ch.end(ctx); err != nil {
				return false, err
			}
		case ok:
			return true, yield(streamRecord{end: true})
		}
	}
	return false, nil
}

// channelStatus 把下行帧 __sm__ 中的 google.rpc.Status 转为生成流记录：带 BardErrorInfo 时取其错误码，状态 16 为会话认证失败
func channelStatus(status any) streamRecord {
	if code, ok := intPath(status, 0, 0, 2, 0, 1, 0); ok {
		return streamRecord{bardCode: code}
	}
	code, _ := intPath(status, 0, 0, 0)
	message, _ := stringPath(status, 0, 0, 1)
	if code == 16 {
		return streamRecord{err: &ProtocolError{
			HTTPStatus: http.StatusUnauthorized, Code: http.StatusUnauthorized, Retryable: true, Cause: ErrSignedOut,
			Message: fmt.Sprintf("生成会话显示未登录（状态 16）: %s", message),
		}}
	}
	encoded, _ := json.Marshal(status)
	return streamRecord{err: retryableProtocolError(fmt.Sprintf("生成会话返回错误状态 %s", encoded), nil)}
}
