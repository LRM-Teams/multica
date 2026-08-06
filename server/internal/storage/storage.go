package storage

import (
	"context"
	"io"
	"time"
)

type Storage interface {
	Upload(ctx context.Context, key string, data []byte, contentType string, filename string) (string, error)
	Delete(ctx context.Context, key string)
	DeleteKeys(ctx context.Context, keys []string)
	KeyFromURL(rawURL string) string
	CdnDomain() string
	// GetReader streams an object back to the caller. Used by the attachment
	// preview proxy (GET /api/attachments/{id}/content) to bypass CloudFront
	// CORS and the inline/attachment Content-Disposition decision. Caller
	// must Close the returned reader.
	GetReader(ctx context.Context, key string) (io.ReadCloser, error)
}

type Presigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type DownloadPresigner interface {
	PresignGetWithContentDisposition(ctx context.Context, key string, ttl time.Duration, contentDisposition string) (string, error)
}

// UploadSessionDestination is the capability returned for a single direct
// upload. URL is empty for storage backends that need the Server's local
// development upload endpoint instead of an external presigned URL.
type UploadSessionDestination struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
}

// UploadedObject is the storage-authoritative object metadata used when an
// Agent upload session is completed. The Server compares it with the session
// declaration before creating an Attachment resource.
type UploadedObject struct {
	URL         string
	SizeBytes   int64
	ContentType string
}

// UploadSessionStorage supports the Agent Upload Session direct-upload and
// completion-verification contract. It intentionally remains separate from
// Storage so unrelated storage test doubles do not accidentally claim that
// they can issue upload capabilities.
type UploadSessionStorage interface {
	PresignUpload(ctx context.Context, key string, ttl time.Duration, contentType, filename string) (UploadSessionDestination, error)
	VerifyUpload(ctx context.Context, key string) (UploadedObject, error)
}
