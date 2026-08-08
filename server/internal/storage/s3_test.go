package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3StorageKeyFromURL_CustomEndpointPreservesNestedKey(t *testing.T) {
	s := &S3Storage{
		bucket:      "test-bucket",
		endpointURL: "http://localhost:9000",
	}

	rawURL := "http://localhost:9000/test-bucket/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestS3StoragePresignGet(t *testing.T) {
	store := &S3Storage{
		client: s3.New(s3.Options{
			Region:      "us-east-1",
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
		}),
		bucket: "test-bucket",
	}

	got, err := store.PresignGet(context.Background(), "uploads/abc/file.txt", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	for _, want := range []string{
		"https://test-bucket.s3.us-east-1.amazonaws.com/uploads/abc/file.txt",
		"X-Amz-Signature=",
		"X-Amz-Expires=300",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("presigned URL %q does not contain %q", got, want)
		}
	}
}

func TestS3StoragePresignGetWithContentDisposition(t *testing.T) {
	store := &S3Storage{
		client: s3.New(s3.Options{
			Region:      "us-east-1",
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
		}),
		bucket: "test-bucket",
	}

	got, err := store.PresignGetWithContentDisposition(
		context.Background(),
		"uploads/abc/file.txt",
		5*time.Minute,
		`attachment; filename="report.txt"`,
	)
	if err != nil {
		t.Fatalf("PresignGetWithContentDisposition: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	if got := u.Query().Get("response-content-disposition"); got != `attachment; filename="report.txt"` {
		t.Fatalf("response-content-disposition = %q", got)
	}
	if sig := u.Query().Get("X-Amz-Signature"); sig == "" {
		t.Fatalf("missing X-Amz-Signature in %q", got)
	}
}

func TestS3StorageKeyFromURL_CustomEndpointWithTrailingSlash(t *testing.T) {
	s := &S3Storage{
		bucket:      "test-bucket",
		endpointURL: "http://localhost:9000/",
	}

	rawURL := "http://localhost:9000/test-bucket/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestS3StorageKeyFromURL_PublicBaseURLPreservesNestedKey(t *testing.T) {
	s := &S3Storage{
		bucket:        "test-bucket",
		publicBaseURL: "https://cdn.example.com/assets",
		endpointURL:   "https://cos.ap-beijing.myqcloud.com",
	}

	rawURL := "https://cdn.example.com/assets/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestS3StorageKeyFromURL_CustomEndpointVirtualHostedStyle(t *testing.T) {
	s := &S3Storage{
		bucket:         "multica-assets",
		endpointURL:    "https://cos.ap-beijing.myqcloud.com",
		forcePathStyle: false,
	}

	rawURL := "https://multica-assets.cos.ap-beijing.myqcloud.com/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestS3StorageKeyFromURL_VirtualHostedStylePreservesNestedKey(t *testing.T) {
	s := &S3Storage{
		bucket: "test-bucket",
		region: "us-east-1",
	}

	rawURL := "https://test-bucket.s3.us-east-1.amazonaws.com/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestS3StorageKeyFromURL_PathStylePreservesNestedKey(t *testing.T) {
	s := &S3Storage{
		bucket: "bucket.with.dots",
		region: "us-east-1",
	}

	rawURL := "https://s3.us-east-1.amazonaws.com/bucket.with.dots/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestS3StorageKeyFromURL_LegacyBucketOnlyHostStillRoundTrips(t *testing.T) {
	// Old records written before the suffix bug was fixed look like
	// "https://<bucket>/<key>". They were broken at fetch time but were still
	// stored, so KeyFromURL must continue to recognise that prefix when we
	// migrate or delete those records.
	s := &S3Storage{
		bucket: "test-bucket",
		region: "us-east-1",
	}

	rawURL := "https://test-bucket/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestLooksLikeS3Hostname(t *testing.T) {
	cases := []struct {
		bucket string
		want   bool
	}{
		{"my-bucket", false},
		{"bucket.with.dots", false},
		{"my-bucket.s3.us-east-1.amazonaws.com", true},
		{"my-bucket.s3.amazonaws.com", true},
		{"s3.us-east-1.amazonaws.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.bucket, func(t *testing.T) {
			if got := looksLikeS3Hostname(tc.bucket); got != tc.want {
				t.Fatalf("looksLikeS3Hostname(%q) = %v, want %v", tc.bucket, got, tc.want)
			}
		})
	}
}

func TestS3StorageUploadedURL(t *testing.T) {
	const key = "uploads/abc/file.png"

	cases := []struct {
		name           string
		bucket         string
		region         string
		cdnDomain      string
		publicBaseURL  string
		endpointURL    string
		forcePathStyle bool
		want           string
	}{
		{
			name:   "default aws virtual hosted style",
			bucket: "test-bucket",
			region: "us-east-1",
			want:   "https://test-bucket.s3.us-east-1.amazonaws.com/uploads/abc/file.png",
		},
		{
			name:   "default aws path style when bucket contains dots",
			bucket: "bucket.with.dots",
			region: "us-east-1",
			want:   "https://s3.us-east-1.amazonaws.com/bucket.with.dots/uploads/abc/file.png",
		},
		{
			name:      "cdn only",
			bucket:    "test-bucket",
			region:    "us-east-1",
			cdnDomain: "cdn.example.com",
			want:      "https://cdn.example.com/uploads/abc/file.png",
		},
		{
			name:           "endpoint only",
			bucket:         "test-bucket",
			region:         "us-east-1",
			endpointURL:    "http://localhost:9000",
			forcePathStyle: true,
			want:           "http://localhost:9000/test-bucket/uploads/abc/file.png",
		},
		{
			name:           "endpoint with trailing slash",
			bucket:         "test-bucket",
			region:         "us-east-1",
			endpointURL:    "http://localhost:9000/",
			forcePathStyle: true,
			want:           "http://localhost:9000/test-bucket/uploads/abc/file.png",
		},
		{
			name:        "endpoint and cdn both set prefers cdn",
			bucket:      "test-bucket",
			region:      "us-east-1",
			cdnDomain:   "cdn.example.com",
			endpointURL: "http://localhost:9000",
			want:        "https://cdn.example.com/uploads/abc/file.png",
		},
		{
			name:          "public base url wins over endpoint and cdn",
			bucket:        "test-bucket",
			region:        "us-east-1",
			cdnDomain:     "cdn.example.com",
			publicBaseURL: "https://assets.example.com/multica",
			endpointURL:   "http://localhost:9000",
			want:          "https://assets.example.com/multica/uploads/abc/file.png",
		},
		{
			name:           "custom endpoint virtual hosted style",
			bucket:         "multica-assets",
			region:         "ap-beijing",
			endpointURL:    "https://cos.ap-beijing.myqcloud.com",
			forcePathStyle: false,
			want:           "https://multica-assets.cos.ap-beijing.myqcloud.com/uploads/abc/file.png",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &S3Storage{
				bucket:         tc.bucket,
				region:         tc.region,
				cdnDomain:      tc.cdnDomain,
				publicBaseURL:  tc.publicBaseURL,
				endpointURL:    tc.endpointURL,
				forcePathStyle: tc.forcePathStyle,
			}
			if got := s.uploadedURL(key); got != tc.want {
				t.Fatalf("uploadedURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestS3StorageCdnDomainPrefersPublicBaseURL(t *testing.T) {
	s := &S3Storage{
		cdnDomain:     "legacy-cdn.example.com",
		publicBaseURL: "https://assets.example.com/multica",
	}

	if got := s.CdnDomain(); got != "assets.example.com" {
		t.Fatalf("CdnDomain() = %q, want %q", got, "assets.example.com")
	}
}

// newMockS3Storage builds an S3Storage whose client talks to an in-process
// S3-compatible mock that only implements HeadObject/GetObject. This lets
// VerifyUpload's fast checksum path and its OSS fallback (body recompute)
// path be exercised without a real bucket.
func newMockS3Storage(t *testing.T, body []byte, headSHA256 string) (*S3Storage, *int32) {
	t.Helper()
	var getCalls int32
	mux := http.NewServeMux()
	// Path-style requests land on /<bucket>/<key>; this mock answers any
	// HEAD with object headers and any GET with the object body.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
			w.Header().Set("Content-Type", "application/octet-stream")
			if headSHA256 != "" {
				w.Header().Set("x-amz-checksum-sha256", headSHA256)
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			atomic.AddInt32(&getCalls, 1)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	client := s3.New(s3.Options{
		Region:       "us-east-1",
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
		BaseEndpoint: aws.String(ts.URL),
		UsePathStyle: true,
	})
	return &S3Storage{
		client:         client,
		bucket:         "test-bucket",
		region:         "us-east-1",
		endpointURL:    ts.URL,
		forcePathStyle: true,
	}, &getCalls
}

func TestS3StorageVerifyUpload_FastPathWithReturnedChecksum(t *testing.T) {
	// Real AWS S3: HeadObject returns the SHA-256 checksum, so VerifyUpload
	// must use it directly and never read the object body.
	body := []byte("hello s3 fast path")
	sum := sha256.Sum256(body)
	store, getCalls := newMockS3Storage(t, body, base64.StdEncoding.EncodeToString(sum[:]))

	obj, err := store.VerifyUpload(context.Background(), "uploads/abc/file.txt")
	if err != nil {
		t.Fatalf("VerifyUpload: %v", err)
	}
	if got := obj.ChecksumSHA256; got != hex.EncodeToString(sum[:]) {
		t.Fatalf("ChecksumSHA256 = %q, want %q", got, hex.EncodeToString(sum[:]))
	}
	if obj.SizeBytes != int64(len(body)) {
		t.Fatalf("SizeBytes = %d, want %d", obj.SizeBytes, len(body))
	}
	if atomic.LoadInt32(getCalls) != 0 {
		t.Fatalf("fast path read object body %d times; want 0", atomic.LoadInt32(getCalls))
	}
}

func TestS3StorageVerifyUpload_OSSFallbackRecomputesChecksum(t *testing.T) {
	// Aliyun OSS: HeadObject returns NO SHA-256 checksum (OSS uses CRC64).
	// VerifyUpload must fall back to reading the body and recomputing sha256.
	body := []byte("hello oss fallback test")
	store, getCalls := newMockS3Storage(t, body, "")

	obj, err := store.VerifyUpload(context.Background(), "uploads/abc/file.txt")
	if err != nil {
		t.Fatalf("VerifyUpload: %v", err)
	}
	sum := sha256.Sum256(body)
	if got := obj.ChecksumSHA256; got != hex.EncodeToString(sum[:]) {
		t.Fatalf("ChecksumSHA256 = %q, want %q", got, hex.EncodeToString(sum[:]))
	}
	if obj.SizeBytes != int64(len(body)) {
		t.Fatalf("SizeBytes = %d, want %d", obj.SizeBytes, len(body))
	}
	if atomic.LoadInt32(getCalls) != 1 {
		t.Fatalf("fallback read object body %d times; want 1", atomic.LoadInt32(getCalls))
	}
}
