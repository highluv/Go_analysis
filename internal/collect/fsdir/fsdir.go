// Package fsdir реализует collect.Reader поверх директории YAML-манифестов.
// Используется для демо и CI без реального кластера (инверсия зависимостей, как kube.Reader).
// Читает все *.yaml / *.yml файлы в директории, парсит каждый документ (поддерживает
// multi-document YAML через разделитель "---"), конвертирует в JSON.
package fsdir

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/highluv/go-analysis/internal/collect"
)

// Reader читает манифесты из директории на диске.
type Reader struct{ dir string }

// New создаёт Reader для указанной директории.
func New(dir string) *Reader { return &Reader{dir: dir} }

func (r *Reader) Read(_ context.Context) ([]collect.RawManifest, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, fmt.Errorf("fsdir: read dir %s: %w", r.dir, err)
	}
	var out []collect.RawManifest
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.dir, name))
		if err != nil {
			return nil, fmt.Errorf("fsdir: read %s: %w", name, err)
		}
		docs := splitDocs(data)
		for _, doc := range docs {
			m, err := parseDoc(doc)
			if err != nil || m == nil {
				continue // пропускаем пустые/невалидные документы
			}
			out = append(out, *m)
		}
	}
	return out, nil
}

// splitDocs разбивает YAML-файл на отдельные документы по разделителю "---".
func splitDocs(data []byte) [][]byte {
	sep := []byte("\n---")
	trimmed := bytes.TrimPrefix(data, []byte("---\n"))
	parts := bytes.Split(trimmed, sep)
	var out [][]byte
	for _, p := range parts {
		p = bytes.TrimSpace(p)
		if len(p) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// parseDoc конвертирует один YAML-документ в RawManifest через YAML→JSON.
func parseDoc(doc []byte) (*collect.RawManifest, error) {
	// sigs.k8s.io/yaml конвертирует YAML в JSON (тот же путь, что и kube-apiserver).
	raw, err := sigsyaml.YAMLToJSON(doc)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}

	var obj struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj.Kind == "" || obj.Metadata.Name == "" {
		return nil, nil
	}

	return &collect.RawManifest{
		APIVersion: obj.APIVersion,
		Kind:       obj.Kind,
		Namespace:  obj.Metadata.Namespace,
		Name:       obj.Metadata.Name,
		Raw:        raw,
	}, nil
}
