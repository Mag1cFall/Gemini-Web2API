package chromeauth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// ErrCredentialsRejected 表示 OAuthMultilogin 以 INVALID_TOKENS 拒绝了保存的续签材料
var ErrCredentialsRejected = errors.New("Google 拒绝了保存的 Chrome 登录材料，在 Chrome 中重新登录该账号后重新导入")

// multiloginStatusError 返回 OAuthMultilogin 某一阶段的失败，INVALID_TOKENS 时包含 ErrCredentialsRejected
func multiloginStatusError(phase string, statusCode int, status string) error {
	if status == "INVALID_TOKENS" {
		return fmt.Errorf("%w（续签 %s 步骤 HTTP %d %s）", ErrCredentialsRejected, phase, statusCode, status)
	}
	if statusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w：Google 续签在 %s 步骤返回 HTTP %d %s", gemini.ErrTransport, phase, statusCode, status)
	}
	return fmt.Errorf("Google 续签在 %s 步骤失败（HTTP %d %s）", phase, statusCode, status)
}

// multiloginURL 是 OAuthMultilogin 的地址
var multiloginURL = "https://accounts.google.com/oauth/multilogin?source=ChromiumAccountReconcilorDice&reuseCookies=0"

// gaiaURL 是解析 DBSC 相对刷新地址的 Gaia 根地址
var gaiaURL = &url.URL{Scheme: "https", Host: "accounts.google.com", Path: "/"}

const (
	oauthClientID     = "77185425430.apps.googleusercontent.com"
	assertionAudience = "https://accounts.google.com/accountmanager"
	assertionSentinel = "DBSC_CHALLENGE_IF_REQUIRED"
)

type multiloginCookie struct {
	Name       string   `json:"name"`
	Value      string   `json:"value"`
	Domain     string   `json:"domain"`
	Host       string   `json:"host"`
	Path       string   `json:"path"`
	MaxAge     *float64 `json:"maxAge"`
	IsHTTPOnly bool     `json:"isHttpOnly"`
	IsSecure   bool     `json:"isSecure"`
	SameSite   string   `json:"sameSite"`
}

type multiloginResponse struct {
	Status         string             `json:"status"`
	Cookies        []multiloginCookie `json:"cookies"`
	Directed       json.RawMessage    `json:"token_binding_directed_response"`
	FailedAccounts []struct {
		Retry struct {
			Challenge string `json:"challenge"`
		} `json:"token_binding_retry_response"`
	} `json:"failed_accounts"`
	BoundSessions []struct {
		IsDeviceBound bool   `json:"is_device_bound"`
		Domain        string `json:"domain"`
		Payload       *struct {
			SessionID   string `json:"session_identifier"`
			RefreshURL  string `json:"refresh_url"`
			Credentials []struct {
				Name string `json:"name"`
			} `json:"credentials"`
		} `json:"register_session_payload"`
	} `json:"device_bound_session_info"`
}

// googleBoundSession 返回响应为 google.com 登记的 DBSC 会话，相对刷新地址按 Gaia 地址解析
func (response multiloginResponse) googleBoundSession() *auth.BoundSession {
	for _, session := range response.BoundSessions {
		if !session.IsDeviceBound || session.Domain != "GOOGLE_COM" || session.Payload == nil {
			continue
		}
		refreshURL, err := url.Parse(session.Payload.RefreshURL)
		if err != nil {
			return nil
		}
		bound := &auth.BoundSession{ID: session.Payload.SessionID, RefreshURL: gaiaURL.ResolveReference(refreshURL).String()}
		for _, credential := range session.Payload.Credentials {
			bound.Credentials = append(bound.Credentials, credential.Name)
		}
		return bound
	}
	return nil
}

// multiloginHeaderOrder 是 Chrome GaiaAuthFetcher 发出 OAuthMultilogin 的请求头顺序
var multiloginHeaderOrder = []string{"content-length", "authorization", "content-type", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "user-agent", "accept-encoding", "accept-language", "priority"}

// browserIdentity 是账号绑定的 Chrome 主版本与 Accept-Language，OAuthMultilogin 以该版本 Chrome 的 TLS、HTTP/2 与请求头发出
type browserIdentity struct {
	chrome   int
	language string
}

type deviceBindingKey interface {
	PublicKey() (*ecdsa.PublicKey, []byte, error)
	SignSHA256([]byte) ([]byte, error)
	Close()
}

// fetchGoogleCookies 用 refresh token 经 OAuthMultilogin 取得 Google Cookie 与 DBSC 会话登记，有设备绑定密钥时走 DBSC 挑战与 HPKE 解密，没有时一次 MultiBearer 请求取得明文 Cookie
func fetchGoogleCookies(ctx context.Context, identity browserIdentity, gaiaID string, token string, wrappedKey []byte, proxyURL string) ([]multiloginCookie, *auth.BoundSession, error) {
	client, err := gemini.NewHTTPClient(30*time.Second, proxyURL, identity.chrome)
	if err != nil {
		return nil, nil, fmt.Errorf("创建 OAuth HTTP 客户端: %w", err)
	}
	defer client.CloseIdleConnections()
	if len(wrappedKey) == 0 {
		cookies, err := fetchBearerCookies(ctx, client, identity, gaiaID, token)
		return cookies, nil, err
	}
	return fetchBoundCookies(ctx, client, identity, gaiaID, token, wrappedKey)
}

// fetchBearerCookies 为没有设备绑定的 token 请求一次 OAuthMultilogin，返回明文 Cookie
func fetchBearerCookies(ctx context.Context, client tls_client.HttpClient, identity browserIdentity, gaiaID string, token string) ([]multiloginCookie, error) {
	status, response, err := requestMultilogin(ctx, client, identity, gaiaID, token, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || response.Status != "OK" {
		return nil, multiloginStatusError("bearer", status, response.Status)
	}
	if err := checkMultiloginCookies(response.Cookies); err != nil {
		return nil, err
	}
	return response.Cookies, nil
}

// fetchBoundCookies 为设备绑定的 token 取得 DBSC challenge、以设备密钥签发 assertion，再解密服务端返回的 Cookie 并提取 DBSC 会话登记
func fetchBoundCookies(ctx context.Context, client tls_client.HttpClient, identity browserIdentity, gaiaID string, token string, wrappedKey []byte) ([]multiloginCookie, *auth.BoundSession, error) {
	bindingKey, err := openDeviceBindingKey(wrappedKey)
	if err != nil {
		return nil, nil, err
	}
	defer bindingKey.Close()
	publicKey, spki, err := bindingKey.PublicKey()
	if err != nil {
		return nil, nil, err
	}

	firstStatus, first, err := requestMultilogin(ctx, client, identity, gaiaID, token, assertionSentinel)
	if err != nil {
		return nil, nil, err
	}
	challenge := findChallenge(first)
	if first.Status != "RETRY" || challenge == "" {
		return nil, nil, multiloginStatusError("challenge", firstStatus, first.Status)
	}

	ephemeralPrivateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("生成 HPKE 临时密钥: %w", err)
	}
	assertion, err := createAssertion(bindingKey, publicKey, spki, challenge, ephemeralPrivateKey.PublicKey().Bytes())
	if err != nil {
		return nil, nil, err
	}
	secondStatus, second, err := requestMultilogin(ctx, client, identity, gaiaID, token, assertion)
	if err != nil {
		return nil, nil, err
	}
	if secondStatus != http.StatusOK || second.Status != "OK" {
		return nil, nil, multiloginStatusError("assertion", secondStatus, second.Status)
	}
	if len(second.Directed) == 0 || bytes.Equal(second.Directed, []byte("null")) {
		return nil, nil, fmt.Errorf("Google 续签响应缺少设备绑定挑战")
	}
	if err := checkMultiloginCookies(second.Cookies); err != nil {
		return nil, nil, err
	}
	for index := range second.Cookies {
		if second.Cookies[index].Value, err = hpkeOpen(ephemeralPrivateKey, second.Cookies[index].Value); err != nil {
			return nil, nil, err
		}
	}
	return second.Cookies, second.googleBoundSession(), nil
}

// checkMultiloginCookies 核对 OAuthMultilogin 返回的 Cookie 包含 SAPISID 与 __Secure-1PSID
func checkMultiloginCookies(cookies []multiloginCookie) error {
	if len(cookies) == 0 {
		return fmt.Errorf("Google 续签响应缺少 Cookie")
	}
	names := make(map[string]struct{}, len(cookies))
	for _, cookie := range cookies {
		names[cookie.Name] = struct{}{}
	}
	for _, required := range []string{"SAPISID", "__Secure-1PSID"} {
		if _, ok := names[required]; !ok {
			return fmt.Errorf("Google 续签返回的 Cookie 缺少 %s", required)
		}
	}
	return nil
}

// requestMultilogin 按 Chrome 的请求头发送一次 OAuthMultilogin：带 assertion 时用 MultiOAuth 头，没有设备绑定的 token 用 MultiBearer 头
func requestMultilogin(ctx context.Context, client tls_client.HttpClient, identity browserIdentity, gaiaID string, token string, assertion string) (int, multiloginResponse, error) {
	authorization := "MultiBearer " + token + ":" + gaiaID
	if assertion != "" {
		authorization = "MultiOAuth " + encodeMultiOAuthHeader(gaiaID, token, assertion)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, multiloginURL, strings.NewReader(" "))
	if err != nil {
		return 0, multiloginResponse{}, fmt.Errorf("创建续签请求: %w", err)
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "none")
	request.Header.Set("Sec-Fetch-Mode", "no-cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("User-Agent", gemini.ChromeUserAgent(identity.chrome))
	request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	request.Header.Set("Accept-Language", identity.language)
	request.Header.Set("Priority", "u=4, i")
	request.Header[http.HeaderOrderKey] = multiloginHeaderOrder
	response, err := client.Do(request)
	if err != nil {
		return 0, multiloginResponse{}, fmt.Errorf("Google 续签请求失败: %w", errors.Join(gemini.ErrTransport, err))
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return response.StatusCode, multiloginResponse{}, fmt.Errorf("读取 Google 续签响应: %w", errors.Join(gemini.ErrTransport, err))
	}
	body = bytes.TrimSpace(body)
	if bytes.HasPrefix(body, []byte(")]}'")) {
		body = bytes.TrimSpace(body[4:])
	}
	var result multiloginResponse
	if err := json.Unmarshal(body, &result); err != nil {
		if response.StatusCode >= http.StatusInternalServerError {
			return response.StatusCode, multiloginResponse{}, fmt.Errorf("%w：Google 续签返回无法解析的响应（HTTP %d）", gemini.ErrTransport, response.StatusCode)
		}
		return response.StatusCode, multiloginResponse{}, fmt.Errorf("Google 续签返回无法解析的响应（HTTP %d）", response.StatusCode)
	}
	return response.StatusCode, result, nil
}

func findChallenge(response multiloginResponse) string {
	for _, account := range response.FailedAccounts {
		if account.Retry.Challenge != "" {
			return account.Retry.Challenge
		}
	}
	return ""
}

func encodeMultiOAuthHeader(gaiaID string, token string, assertion string) string {
	account := appendBytesField(nil, 1, []byte(gaiaID))
	account = appendBytesField(account, 2, []byte(token))
	account = appendBytesField(account, 3, []byte(assertion))
	return base64.RawURLEncoding.EncodeToString(appendBytesField(nil, 1, account))
}

func createTinkHPKEKeyset(publicKey []byte) []byte {
	params := appendVarintField(nil, 1, 1)
	params = appendVarintField(params, 2, 1)
	params = appendVarintField(params, 3, 1)
	hpkePublicKey := appendVarintField(nil, 1, 0)
	hpkePublicKey = appendBytesField(hpkePublicKey, 2, params)
	hpkePublicKey = appendBytesField(hpkePublicKey, 3, publicKey)
	keyData := appendBytesField(nil, 1, []byte("type.googleapis.com/google.crypto.tink.HpkePublicKey"))
	keyData = appendBytesField(keyData, 2, hpkePublicKey)
	keyData = appendVarintField(keyData, 3, 3)
	key := appendBytesField(nil, 1, keyData)
	key = appendVarintField(key, 2, 1)
	key = appendVarintField(key, 3, 1)
	key = appendVarintField(key, 4, 3)
	keyset := appendVarintField(nil, 1, 1)
	return appendBytesField(keyset, 2, key)
}

func createAssertion(bindingKey deviceBindingKey, publicKey *ecdsa.PublicKey, spki []byte, challenge string, ephemeralPublicKey []byte) (string, error) {
	header := struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
		Schema    string `json:"schema"`
	}{"ES256", "jwt", "DEVICE_BOUND_SESSION_CREDENTIALS_ASSERTION"}
	payload := struct {
		Subject      string `json:"sub"`
		Audience     string `json:"aud"`
		JWTID        string `json:"jti"`
		Issuer       string `json:"iss"`
		Namespace    string `json:"namespace"`
		EphemeralKey struct {
			KeyType string `json:"kty"`
			KeyInfo string `json:"TinkKeysetPublicKeyInfo"`
		} `json:"ephemeral_key"`
	}{Subject: oauthClientID, Audience: assertionAudience, JWTID: challenge, Namespace: "TokenBinding"}
	issuer := sha256.Sum256(spki)
	payload.Issuer = base64.RawURLEncoding.EncodeToString(issuer[:])
	payload.EphemeralKey.KeyType = "type.googleapis.com/google.crypto.tink.EciesAeadHkdfPublicKey"
	payload.EphemeralKey.KeyInfo = base64.RawURLEncoding.EncodeToString(createTinkHPKEKeyset(ephemeralPublicKey))
	encodedHeader, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("编码 token binding header: %w", err)
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("编码 token binding payload: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(encodedHeader) + "." + base64.RawURLEncoding.EncodeToString(encodedPayload)
	signature, err := bindingKey.SignSHA256([]byte(signingInput))
	if err != nil {
		return "", err
	}
	if len(signature) != 64 {
		return "", fmt.Errorf("NCrypt ECDSA 签名长度异常")
	}
	digest := sha256.Sum256([]byte(signingInput))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(publicKey, digest[:], r, s) {
		return "", fmt.Errorf("NCrypt ECDSA 签名校验失败")
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func hpkeOpen(privateKey *ecdh.PrivateKey, encodedValue string) (string, error) {
	encrypted, err := base64.RawURLEncoding.DecodeString(encodedValue)
	if err != nil || len(encrypted) <= 48 {
		return "", fmt.Errorf("Google 续签返回的 Cookie 密文格式异常")
	}
	encapsulatedKey := encrypted[:32]
	senderPublicKey, err := ecdh.X25519().NewPublicKey(encapsulatedKey)
	if err != nil {
		return "", fmt.Errorf("解析 HPKE 封装密钥: %w", err)
	}
	sharedDH, err := privateKey.ECDH(senderPublicKey)
	if err != nil {
		return "", fmt.Errorf("计算 HPKE 共享密钥: %w", err)
	}
	kemSuite := append([]byte("KEM"), 0, 0x20)
	hpkeSuite := append([]byte("HPKE"), 0, 0x20, 0, 1, 0, 1)
	eaePRK := labeledExtract(nil, kemSuite, []byte("eae_prk"), sharedDH)
	kemContext := append(append([]byte{}, encapsulatedKey...), privateKey.PublicKey().Bytes()...)
	sharedSecret := labeledExpand(eaePRK, kemSuite, []byte("shared_secret"), kemContext, 32)
	pskIDHash := labeledExtract(nil, hpkeSuite, []byte("psk_id_hash"), nil)
	infoHash := labeledExtract(nil, hpkeSuite, []byte("info_hash"), nil)
	keyScheduleContext := append(append([]byte{0}, pskIDHash...), infoHash...)
	secret := labeledExtract(sharedSecret, hpkeSuite, []byte("secret"), nil)
	key := labeledExpand(secret, hpkeSuite, []byte("key"), keyScheduleContext, 16)
	nonce := labeledExpand(secret, hpkeSuite, []byte("base_nonce"), keyScheduleContext, 12)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("创建 HPKE AES 解密器: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 HPKE GCM 解密器: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, encrypted[32:], []byte{})
	if err != nil {
		return "", fmt.Errorf("Google 续签返回的 Cookie 解密失败")
	}
	return string(plaintext), nil
}

func labeledExtract(salt []byte, suite []byte, label []byte, input []byte) []byte {
	material := append([]byte("HPKE-v1"), suite...)
	material = append(material, label...)
	material = append(material, input...)
	return hkdfExtract(salt, material)
}

func labeledExpand(key []byte, suite []byte, label []byte, info []byte, length int) []byte {
	labeledInfo := make([]byte, 2)
	binary.BigEndian.PutUint16(labeledInfo, uint16(length))
	labeledInfo = append(labeledInfo, []byte("HPKE-v1")...)
	labeledInfo = append(labeledInfo, suite...)
	labeledInfo = append(labeledInfo, label...)
	labeledInfo = append(labeledInfo, info...)
	return hkdfExpand(key, labeledInfo, length)
}

func hkdfExtract(salt []byte, input []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}
	hash := hmac.New(sha256.New, salt)
	hash.Write(input)
	return hash.Sum(nil)
}

func hkdfExpand(key []byte, info []byte, length int) []byte {
	result := make([]byte, 0, length)
	block := []byte(nil)
	for counter := byte(1); len(result) < length; counter++ {
		hash := hmac.New(sha256.New, key)
		hash.Write(block)
		hash.Write(info)
		hash.Write([]byte{counter})
		block = hash.Sum(nil)
		result = append(result, block...)
	}
	return result[:length]
}

func appendBytesField(output []byte, number int, value []byte) []byte {
	output = appendVarint(output, uint64(number<<3|2))
	output = appendVarint(output, uint64(len(value)))
	return append(output, value...)
}

func appendVarintField(output []byte, number int, value uint64) []byte {
	output = appendVarint(output, uint64(number<<3))
	return appendVarint(output, value)
}

func appendVarint(output []byte, value uint64) []byte {
	for value >= 0x80 {
		output = append(output, byte(value)|0x80)
		value >>= 7
	}
	return append(output, byte(value))
}

func parsePublicKeyBlob(blob []byte) (*ecdsa.PublicKey, []byte, error) {
	if len(blob) != 72 || binary.LittleEndian.Uint32(blob[4:8]) != 32 {
		return nil, nil, fmt.Errorf("NCrypt ECDSA 公钥格式异常")
	}
	x := new(big.Int).SetBytes(blob[8:40])
	y := new(big.Int).SetBytes(blob[40:72])
	if !elliptic.P256().IsOnCurve(x, y) {
		return nil, nil, fmt.Errorf("NCrypt ECDSA 公钥曲线异常")
	}
	publicKey := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	spki, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("编码 NCrypt ECDSA 公钥: %w", err)
	}
	return publicKey, spki, nil
}
