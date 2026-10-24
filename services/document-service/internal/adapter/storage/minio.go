// Package storage provides a MinIO adapter for document-service.
package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client wraps a MinIO client with bucket configuration.
type Client struct {
	mc     *minio.Client
	bucket string
}

// New creates a MinIO storage client.
func New(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Client, error) {
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	return &Client{mc: mc, bucket: bucket}, nil
}

// PresignPutURL generates a pre-signed PUT URL for direct client upload.
func (c *Client) PresignPutURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	params := make(url.Values)
	u, err := c.mc.PresignedPutObject(ctx, c.bucket, objectKey, ttl)
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	_ = params
	return u.String(), nil
}

// GetObjectBytes downloads an object and returns its bytes.
func (c *Client) GetObjectBytes(ctx context.Context, objectKey string) ([]byte, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer obj.Close()
	return io.ReadAll(obj)
}

// BuildAssetKey returns the canonical MinIO object key for an uploaded asset.
func BuildAssetKey(tenantID, assetID, filename string) string {
	return fmt.Sprintf("%s/assets/%s/%s", tenantID, assetID, filename)
}
