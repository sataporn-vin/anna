package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultHTTPAddr          = ":8080"
	defaultDatabase          = "personal_memory"
	defaultTimezone          = "Asia/Bangkok"
	defaultMaxCollections    = 100
	defaultMaxRequestBytes   = 1 << 20
	defaultMaxResultRecords  = 100
	defaultMaxResultBytes    = 1 << 20
	defaultMaxPipelineStages = 10
	defaultMongoTimeout      = 5 * time.Second
)

type Config struct {
	HTTPAddr              string
	MongoURI              string
	MongoDatabase         string
	AuthBearerToken       string
	DefaultTimezone       string
	MaxCollections        int
	MaxRequestBytes       int64
	MaxResultRecords      int64
	MaxResultBytes        int
	MaxPipelineStages     int
	MongoOperationTimeout time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:              envOr("HTTP_ADDR", railwayAddr()),
		MongoURI:              os.Getenv("MONGODB_URI"),
		MongoDatabase:         envOr("MONGODB_DATABASE", defaultDatabase),
		AuthBearerToken:       os.Getenv("AUTH_BEARER_TOKEN"),
		DefaultTimezone:       envOr("DEFAULT_TIMEZONE", defaultTimezone),
		MaxCollections:        defaultMaxCollections,
		MaxRequestBytes:       defaultMaxRequestBytes,
		MaxResultRecords:      defaultMaxResultRecords,
		MaxResultBytes:        defaultMaxResultBytes,
		MaxPipelineStages:     defaultMaxPipelineStages,
		MongoOperationTimeout: defaultMongoTimeout,
	}

	var err error
	if cfg.MaxCollections, err = intEnv("MAX_COLLECTIONS", cfg.MaxCollections); err != nil {
		return Config{}, err
	}
	if cfg.MaxRequestBytes, err = int64Env("MAX_REQUEST_BYTES", cfg.MaxRequestBytes); err != nil {
		return Config{}, err
	}
	if cfg.MaxResultRecords, err = int64Env("MAX_RESULT_RECORDS", cfg.MaxResultRecords); err != nil {
		return Config{}, err
	}
	if cfg.MaxResultBytes, err = intEnv("MAX_RESULT_BYTES", cfg.MaxResultBytes); err != nil {
		return Config{}, err
	}
	if cfg.MaxPipelineStages, err = intEnv("MAX_PIPELINE_STAGES", cfg.MaxPipelineStages); err != nil {
		return Config{}, err
	}
	if cfg.MongoOperationTimeout, err = durationEnv("MONGODB_OPERATION_TIMEOUT", cfg.MongoOperationTimeout); err != nil {
		return Config{}, err
	}

	if cfg.MongoURI == "" {
		return Config{}, errors.New("MONGODB_URI is required")
	}
	if cfg.AuthBearerToken == "" {
		return Config{}, errors.New("AUTH_BEARER_TOKEN is required")
	}
	if len(cfg.AuthBearerToken) < 32 {
		return Config{}, errors.New("AUTH_BEARER_TOKEN must contain at least 32 characters")
	}
	if cfg.MaxCollections < 7 || cfg.MaxRequestBytes <= 0 || cfg.MaxResultRecords <= 0 || cfg.MaxResultBytes <= 0 || cfg.MaxPipelineStages <= 0 || cfg.MongoOperationTimeout <= 0 {
		return Config{}, errors.New("all configured limits must be positive and MAX_COLLECTIONS must be at least 7")
	}
	if _, err := time.LoadLocation(cfg.DefaultTimezone); err != nil {
		return Config{}, fmt.Errorf("DEFAULT_TIMEZONE: %w", err)
	}

	return cfg, nil
}

func railwayAddr() string {
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return defaultHTTPAddr
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func int64Env(key string, fallback int64) (int64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
}
