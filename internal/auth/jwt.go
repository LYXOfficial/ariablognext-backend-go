// Package auth 是 JWT 签发与校验（HS256）。
// claims 保持与旧后端一致（sub/iat/exp），方便排查历史 token；旧 token 不要求兼容。
package auth

import (
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/golang-jwt/jwt/v5"
)

// JWT 持有签名密钥与签发时长。
type JWT struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time // 可注入时钟（测试不许用 time.Now 断言）
}

func New(secret string, ttl time.Duration) *JWT {
	return &JWT{secret: []byte(secret), ttl: ttl, now: time.Now}
}

// SetClock 注入固定时钟，仅供测试。
func (j *JWT) SetClock(now func() time.Time) { j.now = now }

// Issue 签发 token。claims：sub=用户名、iat=签发秒、exp=过期秒。
func (j *JWT) Issue(sub string) (string, error) {
	now := j.now()
	claims := jwt.MapClaims{
		"sub": sub,
		"iat": now.Unix(),
		"exp": now.Add(j.ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(j.secret)
	if err != nil {
		return "", apperr.Wrap(apperr.InternalError, "签发凭证失败，请稍后重试", err)
	}
	return signed, nil
}

// Claims 是校验通过后的载荷。
type Claims struct {
	Sub string
	Exp int64
}

// Verify 校验 token（签名 + 过期时间）。
// 兼容「Bearer xxx」与裸 token 两种形态——旧 dashboard 的请求头两种都出现过，
// 剥前缀的动作在这里统一做，调用方拿到的是纯 token。
func (j *JWT) Verify(rawToken string) (*Claims, error) {
	token := strings.TrimPrefix(rawToken, "Bearer ")
	if token == "" {
		return nil, apperr.New(apperr.AuthMissingToken, "缺少登录凭证，请先登录后台", nil)
	}
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		// 算法钉死 HS256：防 alg=none / RS→HS 混淆攻击
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return j.secret, nil
		// 过期判断用注入时钟：与签发侧同一时间基准，测试才能覆盖「签发→过期」全链路
	}, jwt.WithTimeFunc(j.now))
	if err != nil || !parsed.Valid {
		return nil, apperr.New(apperr.AuthInvalidToken, "登录凭证无效或已过期，请重新登录", nil)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, apperr.New(apperr.AuthInvalidToken, "登录凭证无效，请重新登录", nil)
	}
	sub, _ := claims["sub"].(string)
	var exp int64
	if v, ok := claims["exp"].(float64); ok {
		exp = int64(v)
	}
	return &Claims{Sub: sub, Exp: exp}, nil
}
