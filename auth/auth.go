// Package auth 提供 viewer context、密碼雜湊（argon2id）、session token（HMAC）
// 與 API key 的簽發驗證。權限判斷本身在 access package。
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Viewer 是目前請求的操作者身分，由 auth middleware 注入 context。
type Viewer struct {
	ID    int
	Name  string
	Email string
	Role  string
}

type ctxKey int

const (
	viewerKey ctxKey = iota
	systemKey
	contentReaderKey
)

// WithViewer 將 viewer 注入 context。
func WithViewer(ctx context.Context, v *Viewer) context.Context {
	return context.WithValue(ctx, viewerKey, v)
}

// ViewerFrom 取出目前請求的 viewer；未登入回傳 nil。
func ViewerFrom(ctx context.Context) *Viewer {
	v, _ := ctx.Value(viewerKey).(*Viewer)
	return v
}

// WithSystem 標記為系統內部操作（seed、auth 查詢等），跳過權限檢查。
// 絕不可把 system context 交給外部輸入決定的操作。
func WithSystem(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemKey, true)
}

// IsSystem 回報是否為系統內部操作。
func IsSystem(ctx context.Context) bool {
	ok, _ := ctx.Value(systemKey).(bool)
	return ok
}

// ---- 密碼（argon2id）----

const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

// HashPassword 以 argon2id 雜湊密碼。
func HashPassword(plain string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err) // crypto/rand 失敗屬不可恢復
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

// IsHashed 判斷字串是否已是 argon2id 雜湊（hook 用來避免重複雜湊）。
func IsHashed(s string) bool {
	return strings.HasPrefix(s, "$argon2id$")
}

// VerifyPassword 驗證明文密碼與雜湊是否相符。
func VerifyPassword(encoded, plain string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(plain), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---- Session token（HMAC-SHA256，格式 payload.signature）----

// SignToken 簽發 session token，payload 為 "userID.expiryUnix"。
func SignToken(secret []byte, userID int, ttl time.Duration) string {
	payload := fmt.Sprintf("%d.%d", userID, time.Now().Add(ttl).Unix())
	sig := hmacSum(secret, payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// ParseToken 驗證 token 並回傳 userID。
func ParseToken(secret []byte, token string) (int, error) {
	i := strings.LastIndex(token, ".")
	if i < 0 {
		return 0, errors.New("malformed token")
	}
	payloadB, err := base64.RawURLEncoding.DecodeString(token[:i])
	if err != nil {
		return 0, errors.New("malformed token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(token[i+1:])
	if err != nil {
		return 0, errors.New("malformed token")
	}
	payload := string(payloadB)
	if !hmac.Equal(sig, hmacSum(secret, payload)) {
		return 0, errors.New("invalid token signature")
	}
	fields := strings.Split(payload, ".")
	if len(fields) != 2 {
		return 0, errors.New("malformed token payload")
	}
	uid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, errors.New("malformed token payload")
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return 0, errors.New("token expired")
	}
	return uid, nil
}

func hmacSum(secret []byte, payload string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(payload))
	return h.Sum(nil)
}

// ---- API key ----

// NewAPIKey 產生一組 API key，回傳明文（僅此一次）與存 DB 用的雜湊。
func NewAPIKey() (plain, hash string) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	plain = "nlk_" + hex.EncodeToString(buf)
	return plain, HashAPIKey(plain)
}

// HashAPIKey 計算 API key 的 SHA-256 雜湊（hex）。
func HashAPIKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// IsAPIKey 判斷 bearer token 是否為 API key 格式。
func IsAPIKey(token string) bool {
	return strings.HasPrefix(token, "nlk_")
}

// WithContentReader marks a request authenticated by a content:read credential.
// It deliberately carries no CMS user role. All reads use one publication cutoff.
func WithContentReader(ctx context.Context) context.Context {
	return context.WithValue(ctx, contentReaderKey, time.Now())
}

func ContentReadTime(ctx context.Context) (time.Time, bool) {
	t, ok := ctx.Value(contentReaderKey).(time.Time)
	return t, ok
}
