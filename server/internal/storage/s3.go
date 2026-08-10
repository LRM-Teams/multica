package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3Storage struct {
	client         *s3.Client
	bucket         string
	region         string // used to construct virtual-hosted-style public URLs when no CDN/endpoint is set
	cdnDomain      string // if set, returned URLs use this instead of bucket name
	publicBaseURL  string // if set, returned URLs use this full public base URL
	endpointURL    string // if set, use an S3-compatible endpoint instead of AWS S3
	forcePathStyle bool
}

// NewS3StorageFromEnv creates an S3Storage from environment variables.
// Returns nil if S3_BUCKET is not set.
//
// Environment variables:
//   - S3_BUCKET (required)
//   - S3_REGION (default: us-west-2)
//   - AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY (optional; falls back to default credential chain)
//   - AWS_ENDPOINT_URL (optional; for S3-compatible providers)
//   - S3_FORCE_PATH_STYLE (optional; defaults true when AWS_ENDPOINT_URL is set)
//   - S3_PUBLIC_BASE_URL (optional; preferred public/CDN base URL)
//   - CLOUDFRONT_DOMAIN (legacy optional public host)
func NewS3StorageFromEnv() *S3Storage {
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		slog.Info("S3_BUCKET not set, cloud upload disabled")
		return nil
	}
	if looksLikeS3Hostname(bucket) {
		slog.Warn(
			"S3_BUCKET looks like a hostname rather than a bucket name — uploads and public URLs will likely both fail. Use only the bucket name (e.g. \"my-bucket\"), not \"<bucket>.s3.<region>.amazonaws.com\".",
			"value", bucket,
		)
	}

	region := os.Getenv("S3_REGION")
	if region == "" {
		region = "us-west-2"
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}

	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if accessKey != "" && secretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		slog.Error("failed to load AWS config", "error", err)
		return nil
	}

	cdnDomain := strings.Trim(os.Getenv("CLOUDFRONT_DOMAIN"), "/")
	publicBaseURL := strings.TrimRight(os.Getenv("S3_PUBLIC_BASE_URL"), "/")

	endpointURL := os.Getenv("AWS_ENDPOINT_URL")
	// Defaults to path-style whenever a custom endpoint is set — wrong for
	// Aliyun OSS, which needs virtual-hosted-style (S3_FORCE_PATH_STYLE=false)
	// or every PutObject fails with "SecondLevelDomainForbidden". See the
	// full Aliyun OSS gotcha list in .env.example's "S3 / CloudFront" section
	// before touching this default.
	forcePathStyle := endpointURL != ""
	if raw := strings.TrimSpace(os.Getenv("S3_FORCE_PATH_STYLE")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			slog.Warn("invalid S3_FORCE_PATH_STYLE, using default", "value", raw, "default", forcePathStyle)
		} else {
			forcePathStyle = parsed
		}
	}
	s3Opts := []func(*s3.Options){}
	if endpointURL != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpointURL)
			o.UsePathStyle = forcePathStyle
		})
	}

	slog.Info("S3 storage initialized", "bucket", bucket, "region", region, "cdn_domain", cdnDomain, "public_base_url", publicBaseURL, "endpoint_url", endpointURL, "force_path_style", forcePathStyle)
	return &S3Storage{
		client:         s3.NewFromConfig(cfg, s3Opts...),
		bucket:         bucket,
		region:         region,
		cdnDomain:      cdnDomain,
		publicBaseURL:  publicBaseURL,
		endpointURL:    endpointURL,
		forcePathStyle: forcePathStyle,
	}
}

func (s *S3Storage) CdnDomain() string {
	if s.publicBaseURL != "" {
		if u, err := url.Parse(s.publicBaseURL); err == nil {
			return u.Hostname()
		}
	}
	return s.cdnDomain
}

// looksLikeS3Hostname returns true when the configured S3_BUCKET value looks
// like an S3 endpoint hostname rather than a bucket name. Real bucket names
// can never legitimately contain "amazonaws.com", so this is an unambiguous
// misconfiguration signal — the most common form being users pasting
// "<bucket>.s3.<region>.amazonaws.com" into S3_BUCKET.
func looksLikeS3Hostname(bucket string) bool {
	return strings.Contains(bucket, "amazonaws.com")
}

// storageClass returns the appropriate S3 storage class.
// Custom endpoints (e.g. MinIO) only support STANDARD; real AWS defaults to INTELLIGENT_TIERING.
func (s *S3Storage) storageClass() types.StorageClass {
	if s.endpointURL != "" {
		return types.StorageClassStandard
	}
	return types.StorageClassIntelligentTiering
}

// KeyFromURL extracts the S3 object key from a CDN or bucket URL.
// e.g. "https://multica-static.copilothub.ai/abc123.png" → "abc123.png"
//
//	"https://my-bucket.s3.us-east-1.amazonaws.com/uploads/x/y.png" → "uploads/x/y.png"
func (s *S3Storage) KeyFromURL(rawURL string) string {
	if s.publicBaseURL != "" {
		prefix := strings.TrimRight(s.publicBaseURL, "/") + "/"
		if strings.HasPrefix(rawURL, prefix) {
			return strings.TrimPrefix(rawURL, prefix)
		}
	}
	if s.endpointURL != "" {
		prefix := strings.TrimRight(s.endpointURL, "/") + "/" + s.bucket + "/"
		if strings.HasPrefix(rawURL, prefix) {
			return strings.TrimPrefix(rawURL, prefix)
		}
		if !s.forcePathStyle {
			if prefix := s.endpointVirtualHostedPrefix(); prefix != "" && strings.HasPrefix(rawURL, prefix) {
				return strings.TrimPrefix(rawURL, prefix)
			}
		}
	}

	// Strip known "https://host/" prefixes. Order matters: the more specific
	// region-qualified hosts come first so they win over the legacy bucket-only
	// prefix that we used to write before the suffix bug was fixed.
	prefixes := make([]string, 0, 5)
	if s.cdnDomain != "" {
		prefixes = append(prefixes, "https://"+s.cdnDomain+"/")
	}
	if s.region != "" {
		// virtual-hosted-style: https://<bucket>.s3.<region>.amazonaws.com/<key>
		prefixes = append(prefixes,
			"https://"+s.bucket+".s3."+s.region+".amazonaws.com/",
			// path-style: https://s3.<region>.amazonaws.com/<bucket>/<key>
			"https://s3."+s.region+".amazonaws.com/"+s.bucket+"/",
		)
	}
	// Legacy / fallback: the buggy "https://<bucket>/<key>" form that older
	// records may still hold, plus a generic bucket-host prefix.
	prefixes = append(prefixes, "https://"+s.bucket+"/")

	for _, prefix := range prefixes {
		if strings.HasPrefix(rawURL, prefix) {
			return strings.TrimPrefix(rawURL, prefix)
		}
	}
	// Fallback: take everything after the last "/".
	if i := strings.LastIndex(rawURL, "/"); i >= 0 {
		return rawURL[i+1:]
	}
	return rawURL
}

// GetReader streams the object body back to the caller. The returned
// ReadCloser must be closed; closing it terminates the underlying HTTP
// connection to S3. A missing key surfaces as an *types.NoSuchKey error
// wrapped in the SDK's smithy wrapper — callers can use errors.As to
// distinguish "not found" from a transport failure.
func (s *S3Storage) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == "" {
		return nil, fmt.Errorf("s3 GetReader: empty key")
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 GetObject: %w", err)
	}
	return out.Body, nil
}

func (s *S3Storage) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return s.PresignGetWithContentDisposition(ctx, key, ttl, "")
}

func (s *S3Storage) PresignGetWithContentDisposition(ctx context.Context, key string, ttl time.Duration, contentDisposition string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("s3 PresignGet: empty key")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if contentDisposition != "" {
		input.ResponseContentDisposition = aws.String(contentDisposition)
	}
	out, err := s3.NewPresignClient(s.client).PresignGetObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("s3 PresignGetObject: %w", err)
	}
	return out.URL, nil
}

// PresignUpload creates a direct S3 PUT destination for an Agent Upload
// Session. The server keeps the object key and completion-verification
// authority; the Agent receives only this bounded destination.
func (s *S3Storage) PresignUpload(ctx context.Context, key string, ttl time.Duration, contentType, filename, checksumSHA256 string) (UploadSessionDestination, error) {
	if key == "" {
		return UploadSessionDestination{}, fmt.Errorf("s3 PresignUpload: empty key")
	}
	checksum, err := hex.DecodeString(checksumSHA256)
	if err != nil || len(checksum) != sha256.Size {
		return UploadSessionDestination{}, fmt.Errorf("s3 PresignUpload: invalid SHA-256 checksum")
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	out, err := s3.NewPresignClient(s.client).PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(s.bucket),
		Key:                aws.String(key),
		ContentType:        aws.String(contentType),
		ContentDisposition: aws.String(ContentDisposition(contentType, filename)),
		ChecksumSHA256:     aws.String(base64.StdEncoding.EncodeToString(checksum)),
		CacheControl:       aws.String("max-age=432000,public"),
		StorageClass:       s.storageClass(),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = ttl
	})
	if err != nil {
		return UploadSessionDestination{}, fmt.Errorf("s3 PresignPutObject: %w", err)
	}
	headers := make(map[string]string, len(out.SignedHeader))
	for name, values := range out.SignedHeader {
		if strings.EqualFold(name, "Host") || len(values) == 0 {
			continue
		}
		headers[name] = strings.Join(values, ",")
	}
	if _, ok := headers["Content-Type"]; !ok {
		headers["Content-Type"] = contentType
	}
	return UploadSessionDestination{
		URL:     out.URL,
		Method:  "PUT",
		Headers: headers,
	}, nil
}

// VerifyUpload obtains the storage-authoritative metadata after a direct PUT.
func (s *S3Storage) VerifyUpload(ctx context.Context, key string) (UploadedObject, error) {
	if key == "" {
		return UploadedObject{}, fmt.Errorf("s3 VerifyUpload: empty key")
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(key),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return UploadedObject{}, fmt.Errorf("s3 HeadObject: %w", err)
	}
	checksum := ""
	if encoded := aws.ToString(out.ChecksumSHA256); encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(decoded) != sha256.Size {
			return UploadedObject{}, fmt.Errorf("s3 HeadObject: invalid SHA-256 checksum")
		}
		checksum = hex.EncodeToString(decoded)
	}
	// Aliyun OSS and generic S3-compatible endpoints do not implement the AWS
	// S3 checksum model: HeadObject(ChecksumMode=Enabled) does not populate
	// ChecksumSHA256 (OSS uses its own CRC64 instead), so it comes back empty
	// and the agent-upload-session completion compare would produce a
	// checksum_mismatch 409 for every upload. Recompute SHA-256 from the object
	// bytes (same as LocalStorage) whenever the storage-authoritative checksum
	// is unavailable; real AWS S3 still returns it from HeadObject and keeps
	// this cheaper metadata-only path.
	if checksum == "" {
		reader, err := s.GetReader(ctx, key)
		if err != nil {
			return UploadedObject{}, fmt.Errorf("s3 VerifyUpload read: %w", err)
		}
		defer reader.Close()
		hash := sha256.New()
		if _, err := io.Copy(hash, reader); err != nil {
			return UploadedObject{}, fmt.Errorf("s3 VerifyUpload checksum: %w", err)
		}
		checksum = hex.EncodeToString(hash.Sum(nil))
	}
	return UploadedObject{
		URL:            s.uploadedURL(key),
		SizeBytes:      aws.ToInt64(out.ContentLength),
		ContentType:    aws.ToString(out.ContentType),
		ChecksumSHA256: checksum,
	}, nil
}

// Delete removes an object from S3. Errors are logged but not fatal.
func (s *S3Storage) Delete(ctx context.Context, key string) {
	if key == "" {
		return
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		slog.Error("s3 DeleteObject failed", "key", key, "error", err)
	}
}

// DeleteKeys removes multiple objects from S3. Best-effort, errors are logged.
func (s *S3Storage) DeleteKeys(ctx context.Context, keys []string) {
	for _, key := range keys {
		s.Delete(ctx, key)
	}
}

func (s *S3Storage) Upload(ctx context.Context, key string, data []byte, contentType string, filename string) (string, error) {
	return s.upload(ctx, key, data, contentType, filename, "max-age=432000,public")
}

// UploadImmutable publishes an asset under a versioned key with a one-year
// browser/CDN cache. Callers must change the key whenever the bytes change.
func (s *S3Storage) UploadImmutable(ctx context.Context, key string, data []byte, contentType string, filename string) (string, error) {
	return s.upload(ctx, key, data, contentType, filename, "public,max-age=31536000,immutable")
}

func (s *S3Storage) upload(ctx context.Context, key string, data []byte, contentType string, filename string, cacheControl string) (string, error) {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(s.bucket),
		Key:                aws.String(key),
		Body:               bytes.NewReader(data),
		ContentType:        aws.String(contentType),
		ContentDisposition: aws.String(ContentDisposition(contentType, filename)),
		CacheControl:       aws.String(cacheControl),
		StorageClass:       s.storageClass(),
	})
	if err != nil {
		return "", fmt.Errorf("s3 PutObject: %w", err)
	}
	return s.uploadedURL(key), nil
}

// uploadedURL returns the URL stored for client consumption after an upload.
// Priority: public base URL > legacy CDN domain > custom endpoint > AWS S3
// region-qualified host. The public base URL wins even when a custom endpoint
// is set so S3-compatible backends can be paired with a separate public-read
// domain — writes still go through the SDK with the custom endpoint; only the
// reader-facing URL changes.
//
// For the default AWS S3 case, virtual-hosted-style is preferred:
// https://<bucket>.s3.<region>.amazonaws.com/<key>. When the bucket name
// contains dots, the AWS-issued wildcard TLS certificate (`*.s3.amazonaws.com`)
// fails to validate the host, so we fall back to path-style:
// https://s3.<region>.amazonaws.com/<bucket>/<key>.
func (s *S3Storage) uploadedURL(key string) string {
	if s.publicBaseURL != "" {
		return fmt.Sprintf("%s/%s", strings.TrimRight(s.publicBaseURL, "/"), key)
	}
	if s.cdnDomain != "" {
		return fmt.Sprintf("https://%s/%s", s.cdnDomain, key)
	}
	if s.endpointURL != "" {
		if !s.forcePathStyle {
			if prefix := s.endpointVirtualHostedPrefix(); prefix != "" {
				return prefix + key
			}
		}
		return fmt.Sprintf("%s/%s/%s", strings.TrimRight(s.endpointURL, "/"), s.bucket, key)
	}
	if strings.Contains(s.bucket, ".") {
		return fmt.Sprintf("https://s3.%s.amazonaws.com/%s/%s", s.region, s.bucket, key)
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.bucket, s.region, key)
}

func (s *S3Storage) endpointVirtualHostedPrefix() string {
	if s.endpointURL == "" || s.bucket == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimRight(s.endpointURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.Host = s.bucket + "." + parsed.Host
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
