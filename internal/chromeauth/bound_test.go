package chromeauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
)

// softwareBindingKey 是以内存 P-256 私钥签发 IEEE P1363 签名的设备绑定密钥替身
type softwareBindingKey struct {
	privateKey *ecdsa.PrivateKey
}

func (k softwareBindingKey) PublicKey() ([]byte, error) {
	return x509.MarshalPKIXPublicKey(&k.privateKey.PublicKey)
}

func (k softwareBindingKey) SignSHA256(value []byte) ([]byte, error) {
	digest := sha256.Sum256(value)
	r, s, err := ecdsa.Sign(rand.Reader, k.privateKey, digest[:])
	if err != nil {
		return nil, err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signature, nil
}

func (softwareBindingKey) Close() {}

// TestSignBoundRefresh 验证 DBSC 挑战提取与刷新证明签名符合 W3C DBSC 规范及 Chrome RotateBoundCookies 现场
func TestSignBoundRefresh(t *testing.T) {
	challenge := boundChallenge(`"other-challenge";id="yt_session", "dbsc-challenge-token";id="sidts_session"`, "sidts_session")
	if challenge != "dbsc-challenge-token" {
		t.Fatalf("challenge = %q, want %q", challenge, "dbsc-challenge-token")
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signBoundRefresh(softwareBindingKey{privateKey: privateKey}, challenge)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(signed, ".")
	if len(parts) != 3 {
		t.Fatalf("JWS 段数 = %d, want 3", len(parts))
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("解码 JWS header: %v", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("解码 JWS payload: %v", err)
	}
	if string(header) != `{"alg":"ES256","typ":"dbsc+jwt"}` {
		t.Fatalf("header = %s, want %s", header, `{"alg":"ES256","typ":"dbsc+jwt"}`)
	}
	if string(payload) != `{"jti":"dbsc-challenge-token"}` {
		t.Fatalf("payload = %s, want %s", payload, `{"jti":"dbsc-challenge-token"}`)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		t.Fatalf("signature len = %d, err = %v", len(signature), err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(&privateKey.PublicKey, digest[:], r, s) {
		t.Fatal("ECDSA P-256 签名无法以绑定公钥验证")
	}
}
