// Package collect определяет ПОРТ сбора манифестов из источника.
//
// Зачем интерфейс, а не сразу client-go: это инверсия зависимостей (одна из главных
// архитектурных идей проекта). Источник подменяем:
//   - collect/kube   — реальный кластер через client-go (нужен k3d);
//   - collect/fsdir  — директория YAML-манифестов (демо/CI без кластера);
//   - SliceReader    — фиксированный срез (юнит/интеграционные тесты).
//
// Благодаря этому весь pipeline (collect → store → normalize → analyze → API)
// прогоняется и тестируется без кластера, а client-go изолирован в одном пакете.
package collect

import "context"

// RawManifest — один собранный объект как сырой JSON плюс координаты для индексации.
// JSON выбран форматом raw намеренно: это то, на чём нативно говорит Kubernetes API,
// а значит нормализатор парсит его stdlib-ом encoding/json без внешних зависимостей.
type RawManifest struct {
	APIVersion string
	Kind       string
	Namespace  string // '' для cluster-scoped (Namespace и т.п.)
	Name       string
	Raw        []byte // тело объекта в JSON
}

// Reader читает состояние источника целиком (collect cluster-wide — зафиксировано планом).
type Reader interface {
	Read(ctx context.Context) ([]RawManifest, error)
}

// SliceReader — тривиальный Reader поверх среза. Используется в тестах и для импорта.
type SliceReader struct{ Items []RawManifest }

func (s SliceReader) Read(context.Context) ([]RawManifest, error) { return s.Items, nil }
