// Package miniostore — реализация store.Blob поверх MinIO/S3 (minio-go/v7).
// Здесь живут «тяжёлые байты» сырых манифестов (AP-5): объектное хранилище дешевле и
// естественнее для произвольных blob'ов, чем реляционная БД.
package miniostore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/highluv/go-analysis/internal/store"
)

type Blob struct {
	client *minio.Client
	bucket string
}

var _ store.Blob = (*Blob)(nil)

type Options struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

// New создаёт клиент MinIO и гарантирует существование бакета.
func New(ctx context.Context, o Options) (*Blob, error) {
	client, err := minio.New(o.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(o.AccessKey, o.SecretKey, ""),
		Secure: o.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	exists, err := client.BucketExists(ctx, o.Bucket)
	if err != nil {
		return nil, fmt.Errorf("bucket exists: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, o.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("make bucket %s: %w", o.Bucket, err)
		}
	}
	return &Blob{client: client, bucket: o.Bucket}, nil
}

func (b *Blob) Put(ctx context.Context, key string, data []byte) error {
	_, err := b.client.PutObject(ctx, b.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/json"})
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	return nil
}

func (b *Blob) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := b.client.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.Code == "NoSuchKey" {
			return nil, fmt.Errorf("blob %q не найден", key)
		}
		return nil, fmt.Errorf("read object %s: %w", key, err)
	}
	return data, nil
}
