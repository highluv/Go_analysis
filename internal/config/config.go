// Package config собирает конфигурацию из переменных окружения.
// В Kubernetes несекретные значения приходят из ConfigMap, секреты (пароль БД, ключи MinIO) — из Secret;
// и то и другое монтируется в env. Поэтому единый источник — os.Getenv с дефолтами.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	HTTPAddr string // адрес HTTP-сервера, напр. ":8080"

	StoreKind string // "memory" | "postgres"
	BlobKind  string // "memory" | "minio"

	// Postgres
	PostgresDSN string // postgres://user:pass@host:5432/db?sslmode=disable

	// MinIO / S3
	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioBucket    string
	MinioUseSSL    bool

	// Источник сбора по умолчанию для acgd: "cluster" | "dir:<path>"
	DefaultSource string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load читает конфигурацию из окружения, подставляя разумные дефолты для локального запуска.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:       getenv("ACG_HTTP_ADDR", ":8080"),
		StoreKind:      getenv("ACG_STORE", "memory"),
		BlobKind:       getenv("ACG_BLOB", "memory"),
		PostgresDSN:    getenv("ACG_POSTGRES_DSN", ""),
		MinioEndpoint:  getenv("ACG_MINIO_ENDPOINT", ""),
		MinioAccessKey: getenv("ACG_MINIO_ACCESS_KEY", ""),
		MinioSecretKey: getenv("ACG_MINIO_SECRET_KEY", ""),
		MinioBucket:    getenv("ACG_MINIO_BUCKET", "acg-raw"),
		MinioUseSSL:    getenv("ACG_MINIO_USE_SSL", "false") == "true",
		DefaultSource:  getenv("ACG_SOURCE", "dir:./test/golden"),
	}

	// Валидация связности конфигурации: выбранные адаптеры требуют своих параметров.
	if c.StoreKind == "postgres" && c.PostgresDSN == "" {
		return c, fmt.Errorf("ACG_STORE=postgres требует ACG_POSTGRES_DSN")
	}
	if c.BlobKind == "minio" {
		if c.MinioEndpoint == "" || c.MinioAccessKey == "" || c.MinioSecretKey == "" {
			return c, fmt.Errorf("ACG_BLOB=minio требует ACG_MINIO_ENDPOINT, ACG_MINIO_ACCESS_KEY, ACG_MINIO_SECRET_KEY")
		}
	}
	return c, nil
}
