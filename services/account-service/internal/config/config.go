package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server   ServerConfig
	Auth     AuthConfig
	DB       DBConfig
	Redis    RedisConfig
	CORS     CORSConfig
	Rate     RateLimitConfig
	Swagger  SwaggerConfig
	Security SecurityConfig
	Email    EmailConfig
	Google   GoogleOAuthConfig
}

type GoogleOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Enabled      bool
}

type ServerConfig struct {
	Port string
	Env  string
}

type AuthConfig struct {
	JWTSecret              string
	JWTExpiryHours         time.Duration
	RefreshTokenExpiryDays time.Duration
	EmailVerifyExpiryHours time.Duration
	PasswordResetExpiryMin time.Duration
}

type DBConfig struct {
	DSN string
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type CORSConfig struct {
	AllowedOrigins []string
}

type RateLimitConfig struct {
	RPS   int
	Burst int
}

type SwaggerConfig struct {
	Enabled bool
}

type SecurityConfig struct {
	MaxLoginAttempts int
	LockoutMinutes   int
	BcryptCost       int
	InternalAPIKey   string
}

type EmailConfig struct {
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	FromAddress  string
	Enabled      bool
}

func Load() (*Config, error) {
	cfg := &Config{}

	cfg.Server.Port = getEnv("PORT", "6000")
	cfg.Server.Env = getEnv("ENV", "development")

	cfg.Auth.JWTSecret = mustGetEnv("JWT_SECRET")

	expiryH, _ := strconv.Atoi(getEnv("JWT_EXPIRY_HOURS", "24"))
	cfg.Auth.JWTExpiryHours = time.Duration(expiryH) * time.Hour

	refreshD, _ := strconv.Atoi(getEnv("REFRESH_TOKEN_EXPIRY_DAYS", "7"))
	cfg.Auth.RefreshTokenExpiryDays = time.Duration(refreshD) * 24 * time.Hour

	emailVerifyH, _ := strconv.Atoi(getEnv("EMAIL_VERIFY_EXPIRY_HOURS", "24"))
	cfg.Auth.EmailVerifyExpiryHours = time.Duration(emailVerifyH) * time.Hour

	pwResetM, _ := strconv.Atoi(getEnv("PASSWORD_RESET_EXPIRY_MINUTES", "30"))
	cfg.Auth.PasswordResetExpiryMin = time.Duration(pwResetM) * time.Minute

	cfg.DB.DSN = mustGetEnv("POSTGRES_DSN")

	cfg.Redis.Addr = getEnv("REDIS_ADDR", "localhost:6379")
	cfg.Redis.Password = getEnv("REDIS_PASSWORD", "")
	cfg.Redis.DB, _ = strconv.Atoi(getEnv("REDIS_DB", "0"))

	originsRaw := getEnv("ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000,http://localhost:8000,http://127.0.0.1:8000")
	cfg.CORS.AllowedOrigins = parseAllowedOrigins(originsRaw)

	cfg.Rate.RPS, _ = strconv.Atoi(getEnv("RATE_LIMIT_RPS", "100"))
	cfg.Rate.Burst, _ = strconv.Atoi(getEnv("RATE_LIMIT_BURST", "20"))

	cfg.Swagger.Enabled = getEnv("SWAGGER_ENABLED", "true") == "true"

	cfg.Security.MaxLoginAttempts, _ = strconv.Atoi(getEnv("MAX_LOGIN_ATTEMPTS", "5"))
	cfg.Security.LockoutMinutes, _ = strconv.Atoi(getEnv("LOCKOUT_MINUTES", "15"))
	cfg.Security.BcryptCost, _ = strconv.Atoi(getEnv("BCRYPT_COST", "12"))
	cfg.Security.InternalAPIKey = mustGetEnv("INTERNAL_API_KEY")

	cfg.Email.SMTPHost = getEnv("SMTP_HOST", "")
	cfg.Email.SMTPPort, _ = strconv.Atoi(getEnv("SMTP_PORT", "587"))
	cfg.Email.SMTPUser = getEnv("SMTP_USER", "")
	cfg.Email.SMTPPassword = getEnv("SMTP_PASSWORD", "")
	cfg.Email.FromAddress = getEnv("SMTP_FROM", "noreply@ethioscript.io")
	cfg.Email.Enabled = cfg.Email.SMTPHost != ""

	cfg.Google.ClientID = getEnv("GOOGLE_CLIENT_ID", "")
	cfg.Google.ClientSecret = getEnv("GOOGLE_CLIENT_SECRET", "")
	cfg.Google.RedirectURL = getEnv("GOOGLE_REDIRECT_URL", "http://localhost:8000/auth/google/callback")
	cfg.Google.Enabled = cfg.Google.ClientID != ""

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseAllowedOrigins(raw string) []string {
	origins := strings.Split(raw, ",")
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}
	return origins
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required env var %q is not set", key))
	}
	return v
}
