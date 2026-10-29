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
	DB       DBConfig
	RabbitMQ RabbitMQConfig
	Auth     AuthConfig
	CORS     CORSConfig
	VAPID    VAPIDConfig
	OTP      OTPConfig
	Outbox   OutboxConfig
	Swagger  SwaggerConfig
}

type ServerConfig struct {
	Port string
	Env  string
}

type DBConfig struct {
	DSN string
}

type RabbitMQConfig struct {
	URL      string
	Exchange string
}

type AuthConfig struct {
	JWTSecret      string
	InternalAPIKey string
}

type CORSConfig struct {
	AllowedOrigins []string
}

type VAPIDConfig struct {
	PublicKey  string
	PrivateKey string
	Subject    string
}

type OTPConfig struct {
	ExpiryMinutes int
	Length        int
}

type OutboxConfig struct {
	PollSeconds int
}

type SwaggerConfig struct {
	Enabled bool
}

func Load() (*Config, error) {
	cfg := &Config{}

	cfg.Server.Port = getEnv("PORT", "6007")
	cfg.Server.Env = getEnv("ENV", "development")

	cfg.DB.DSN = mustGetEnv("POSTGRES_DSN")

	cfg.RabbitMQ.URL = getEnv("RABBITMQ_URL", "amqp://ethio:ethiopass@rabbitmq:5672/ethioscript")
	cfg.RabbitMQ.Exchange = getEnv("RABBITMQ_EXCHANGE", "ethioscript.events")

	cfg.Auth.JWTSecret = mustGetEnv("JWT_SECRET")
	cfg.Auth.InternalAPIKey = mustGetEnv("INTERNAL_API_KEY")

	originsRaw := getEnv("ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000")
	cfg.CORS.AllowedOrigins = parseList(originsRaw)

	cfg.VAPID.PublicKey = getEnv("VAPID_PUBLIC_KEY", "")
	cfg.VAPID.PrivateKey = getEnv("VAPID_PRIVATE_KEY", "")
	cfg.VAPID.Subject = getEnv("VAPID_CLAIMS_SUB", "mailto:api@ethioscript.io")

	cfg.OTP.ExpiryMinutes, _ = strconv.Atoi(getEnv("OTP_EXPIRY_MINUTES", "10"))
	cfg.OTP.Length, _ = strconv.Atoi(getEnv("OTP_LENGTH", "6"))

	cfg.Outbox.PollSeconds, _ = strconv.Atoi(getEnv("OUTBOX_POLL_SECONDS", "5"))

	cfg.Swagger.Enabled = getEnv("SWAGGER_ENABLED", "true") == "true"

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

func parseList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// OTPExpiry returns the OTP expiry as a time.Duration.
func (c *Config) OTPExpiry() time.Duration {
	return time.Duration(c.OTP.ExpiryMinutes) * time.Minute
}

// OutboxPoll returns the outbox poll interval as a time.Duration.
func (c *Config) OutboxPoll() time.Duration {
	return time.Duration(c.Outbox.PollSeconds) * time.Second
}
