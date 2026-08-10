package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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

func TestS3StorageUploadImmutableSetsVersionedAssetCachePolicy(t *testing.T) {
	t.Parallel()

	var gotCacheControl string
	var gotContentType string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gotCacheControl = r.Header.Get("Cache-Control")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := &S3Storage{
		client: s3.New(s3.Options{
			Region:       "cn-beijing",
			Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
			BaseEndpoint: aws.String(srv.URL),
			UsePathStyle: true,
			HTTPClient:   srv.Client(),
		}),
		bucket:         "test-bucket",
		region:         "cn-beijing",
		endpointURL:    srv.URL,
		forcePathStyle: true,
		publicBaseURL:  "https://cdn.example.com",
	}

	gotURL, err := store.UploadImmutable(
		context.Background(),
		"agent-avatars/v2/agent-01.png",
		[]byte("png"),
		"image/png",
		"agent-01.png",
	)
	if err != nil {
		t.Fatalf("UploadImmutable: %v", err)
	}
	if gotURL != "https://cdn.example.com/agent-avatars/v2/agent-01.png" {
		t.Fatalf("URL = %q", gotURL)
	}
	if gotCacheControl != "public,max-age=31536000,immutable" {
		t.Fatalf("Cache-Control = %q", gotCacheControl)
	}
	if gotContentType != "image/png" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
}

// TestS3StorageVerifyUpload_RecomputesChecksumWhenAWSChecksumMissing guards the
// OSS / S3-compatible fix: Aliyun OSS does not populate HeadObject
// ChecksumSHA256 (it uses CRC64), so VerifyUpload must fall back to reading the
// object bytes and recomputing sha256 (same as LocalStorage). A real AWS
// backend keeps the metadata path (covers the non-empty checksum case).
func TestS3StorageVerifyUpload_RecomputesChecksumWhenAWSChecksumMissing(t *testing.T) {
	payload := []byte("hello oss upload content")
	sum := sha256.Sum256(payload)
	wantChecksum := hex.EncodeToString(sum[:])

	var gotGet bool
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// GetObject: return the object body (used by the recompute fallback).
			gotGet = true
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
		default:
			// HeadObject: metadata only, deliberately no x-amz-checksum-sha256
			// header — simulates Aliyun OSS / a non-AWS S3-compatible endpoint.
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := &S3Storage{
		client: s3.New(s3.Options{
			Region:       "cn-hangzhou",
			Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
			BaseEndpoint: aws.String(srv.URL),
			UsePathStyle: true,
			HTTPClient:   srv.Client(),
		}),
		bucket:         "test-bucket",
		region:         "cn-hangzhou",
		endpointURL:    srv.URL, // non-AWS S3-compatible (OSS-like) endpoint
		forcePathStyle: true,
		publicBaseURL:  "https://cdn.example.com",
	}

	obj, err := store.VerifyUpload(context.Background(), "uploads/abc/file.txt")
	if err != nil {
		t.Fatalf("VerifyUpload: %v", err)
	}
	if obj.ChecksumSHA256 != wantChecksum {
		t.Fatalf("ChecksumSHA256 = %q, want %q (recomputed from bytes)", obj.ChecksumSHA256, wantChecksum)
	}
	if obj.SizeBytes != int64(len(payload)) {
		t.Fatalf("SizeBytes = %d, want %d", obj.SizeBytes, len(payload))
	}
	if obj.ContentType != "text/plain" {
		t.Fatalf("ContentType = %q, want %q", obj.ContentType, "text/plain")
	}
	if !gotGet {
		t.Fatal("expected GetObject (read-back) to be exercised for the OSS checksum fallback")
	}
}

// TestS3StorageVerifyUpload_KeepsAWSChecksumWhenPresent guards that a real AWS S3
// backend is not regressed: when HeadObject returns ChecksumSHA256, VerifyUpload
// uses the storage-authoritative metadata checksum and does NOT fall back to a
// read-back (GetObject) of the whole object.
func TestS3StorageVerifyUpload_KeepsAWSChecksumWhenPresent(t *testing.T) {
	payload := []byte("aws checksum payload")
	sum := sha256.Sum256(payload)
	wantChecksum := hex.EncodeToString(sum[:])
	checksumHeader := base64.StdEncoding.EncodeToString(sum[:])

	var gotGet bool
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Should NOT be called on the AWS path — the metadata checksum wins.
			gotGet = true
			w.WriteHeader(http.StatusOK)
		default:
			// HeadObject: AWS returns the SHA-256 checksum in x-amz-checksum-sha256.
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Header().Set("x-amz-checksum-sha256", checksumHeader)
			w.WriteHeader(http.StatusOK)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := &S3Storage{
		client: s3.New(s3.Options{
			Region:       "us-east-1",
			Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
			BaseEndpoint: aws.String(srv.URL),
			UsePathStyle: true,
			HTTPClient:   srv.Client(),
		}),
		bucket:         "test-bucket",
		region:         "us-east-1",
		endpointURL:    "", // real AWS S3 (no custom endpoint)
		forcePathStyle: true,
		publicBaseURL:  "https://cdn.example.com",
	}

	obj, err := store.VerifyUpload(context.Background(), "uploads/abc/file.txt")
	if err != nil {
		t.Fatalf("VerifyUpload: %v", err)
	}
	if obj.ChecksumSHA256 != wantChecksum {
		t.Fatalf("ChecksumSHA256 = %q, want %q (from HeadObject metadata)", obj.ChecksumSHA256, wantChecksum)
	}
	if gotGet {
		t.Fatal("expected GetObject NOT to be called when HeadObject returns a checksum (AWS path)")
	}
}
