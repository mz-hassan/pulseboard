package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port            string
	RedisAddr       string
	RedisPassword   string
	RoomTTL         time.Duration
	AllowedOrigins  map[string]struct{}
	WriteWait       time.Duration
	PongWait        time.Duration
	MaxMessageBytes int64
	MaxRoomUsers    int
}

func Load() Config {
	return Config{
		Port:            env("PORT", "8080"),
		RedisAddr:       env("REDIS_ADDR", "localhost:6379"),
		RedisPassword:   os.Getenv("REDIS_PASSWORD"),
		RoomTTL:         duration("ROOM_TTL", 7*24*time.Hour),
		AllowedOrigins:  origins(env("ALLOWED_ORIGINS", "http://localhost:8080")),
		WriteWait:       duration("WRITE_WAIT", 10*time.Second),
		PongWait:        duration("PONG_WAIT", 60*time.Second),
		MaxMessageBytes: int64(integer("MAX_MESSAGE_BYTES", 64*1024)),
		MaxRoomUsers:    integer("MAX_ROOM_USERS", 250),
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func integer(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func duration(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func origins(raw string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}
