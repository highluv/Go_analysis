-- 002_workload_images.sql
-- Добавляет колонку images (text[]) в таблицу workload для хранения образов контейнеров.

-- +goose Up
ALTER TABLE workload ADD COLUMN IF NOT EXISTS images text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE workload DROP COLUMN IF EXISTS images;
