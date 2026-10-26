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
	mc        *minio.Client
	bucket    string
	publicURL string // browser-accessible base URL e.g. "http://localhost:6302"
}

// New creates a MinIO storage client.
// publicURL is the externally-accessible URL used in presigned URLs (e.g. http://localhost:6302).
func New(endpoint, accessKey, secretKey, bucket, publicURL string, useSSL bool) (*Client, error) {
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	if publicURL == "" {
		publicURL = "http://" + endpoint
	}
	return &Client{mc: mc, bucket: bucket, publicURL: publicURL}, nil
}

// PresignPutURL generates a pre-signed PUT URL using the public (browser-accessible) endpoint.
func (c *Client) PresignPutURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	u, err := c.mc.PresignedPutObject(ctx, c.bucket, objectKey, ttl)
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	// Replace internal Docker hostname with public URL so the browser can reach it
	raw := u.String()
	if c.publicURL != "" {
		parsed, perr := url.Parse(raw)
		if perr == nil {
			pub, perr2 := url.Parse(c.publicURL)
			if perr2 == nil {
				parsed.Scheme = pub.Scheme
				parsed.Host = pub.Host
				raw = parsed.String()
			}
		}
	}
	return raw, nil
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
