package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/ethioscript/account-service/internal/domain"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken = errors.New("invalid or expired token")
)

// JWTClaims wraps standard JWT claims with EthioScript identity.
type JWTClaims struct {
	UserID   string `json:"uid"`
	TenantID string `json:"tid"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Name     string `json:"name"`
	jwt.RegisteredClaims
}

// Manager handles JWT creation and validation.
type Manager struct {
	secret        []byte
	expiryHours   time.Duration
	refreshExpiry time.Duration
}

func NewManager(secret string, expiryHours, refreshExpiry time.Duration) *Manager {
	return &Manager{
		secret:        []byte(secret),
		expiryHours:   expiryHours,
		refreshExpiry: refreshExpiry,
	}
}

// GenerateAccessToken mints a signed JWT.
func (m *Manager) GenerateAccessToken(c domain.Claims) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(m.expiryHours)

	claims := JWTClaims{
		UserID:   c.UserID.String(),
		TenantID: c.TenantID.String(),
		Email:    c.Email,
		Role:     string(c.Role),
		Name:     c.Name,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "ethioscript-account-service",
			Subject:   c.UserID.String(),
			ID:        uuid.NewString(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	return signed, exp, err
}

// ValidateAccessToken parses and validates a JWT string.
func (m *Manager) ValidateAccessToken(tokenStr string) (*domain.Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &JWTClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))

	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}

	jc, ok := token.Claims.(*JWTClaims)
	if !ok {
		return nil, ErrInvalidToken
	}

	userID, err := uuid.Parse(jc.UserID)
	if err != nil {
		return nil, ErrInvalidToken
	}
	tenantID, err := uuid.Parse(jc.TenantID)
	if err != nil {
		return nil, ErrInvalidToken
	}

	return &domain.Claims{
		UserID:   userID,
		TenantID: tenantID,
		Email:    jc.Email,
		Role:     domain.UserRole(jc.Role),
		Name:     jc.Name,
	}, nil
}

// GenerateOpaqueToken returns a cryptographically random 64-hex-char token.
func GenerateOpaqueToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	raw = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(h[:])
	return
}

// HashToken returns the SHA-256 hex hash of a raw token string.
func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
