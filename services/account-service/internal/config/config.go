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
	MaxLoginAttempts  int
	LockoutMinutes    int
	BcryptCost        int
	InternalAPIKey    string
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

	originsRaw := getEnv("ALLOWED_ORIGINS", "http://localhost:3000")
	cfg.CORS.AllowedOrigins = strings.Split(originsRaw, ",")

	cfg.Rate.RPS, _ = strconv.Atoi(getEnv("RATE_LIMIT_RPS", "100"))
	cfg.Rate.Burst, _ = strconv.Atoi(getEnv("RATE_LIMIT_BURST", "20"))

	cfg.Swagger.Enabled = getEnv("SWAGGER_ENABLED", "true") == "true"

	cfg.Security.MaxLoginAttempts, _ = strconv.Atoi(getEnv("MAX_LOGIN_ATTEMPTS", "5"))
	cfg.Security.LockoutMinutes, _ = strconv.Atoi(getEnv("LOCKOUT_MINUTES", "15"))
	cfg.Security.BcryptCost, _ = strconv.Atoi(getEnv("BCRYPT_COST", "12"))
	cfg.Security.InternalAPIKey = mustGetEnv("INTERNAL_API_KEY")

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required env var %q is not set", key))
	}
	return v
}
