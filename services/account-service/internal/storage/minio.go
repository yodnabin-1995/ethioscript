// Package storage provides MinIO operations for the account-service.
package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client wraps a MinIO client.
type Client struct {
	mc     *minio.Client
	bucket string
}

// New connects to MinIO.
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

// PutObject uploads raw bytes with the given content type and returns the object key.
func (c *Client) PutObject(ctx context.Context, objectKey, contentType string, data io.Reader, size int64) error {
	_, err := c.mc.PutObject(ctx, c.bucket, objectKey, data, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("minio put: %w", err)
	}
	return nil
}

// PublicURL returns a browser-accessible URL to an object.
// Uses publicEndpoint (e.g. localhost:6302) not the internal Docker hostname.
func (c *Client) PublicURL(publicEndpoint, objectKey string) string {
	scheme := "http"
	return fmt.Sprintf("%s://%s/%s/%s", scheme, publicEndpoint, c.bucket, objectKey)
}

// ProfilePhotoKey returns the canonical MinIO key for a user's profile photo.
func ProfilePhotoKey(tenantID, userID, ext string) string {
	return fmt.Sprintf("%s/profiles/%s/photo%s", tenantID, userID, ext)
}
