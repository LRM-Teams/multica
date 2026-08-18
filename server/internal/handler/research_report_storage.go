package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
	"github.com/multica-ai/multica/server/internal/storage"
)

type researchReportStorageAdapter struct{ store storage.Storage }

func (a researchReportStorageAdapter) CreateImmutableUpload(ctx context.Context, key string, declaration researchrun.ReportUploadDeclaration, ttl time.Duration) (researchrun.ReportUploadCapability, error) {
	uploads, ok := a.store.(storage.UploadSessionStorage)
	if !ok {
		return researchrun.ReportUploadCapability{}, fmt.Errorf("report upload storage unavailable")
	}
	destination, err := uploads.PresignUpload(ctx, key, ttl, declaration.MediaType, declaration.Path, strings.TrimPrefix(declaration.ContentHash, "sha256:"))
	if err != nil {
		return researchrun.ReportUploadCapability{}, err
	}
	return researchrun.ReportUploadCapability{Method: destination.Method, URL: destination.URL, Headers: destination.Headers, ExpiresAt: time.Now().Add(ttl)}, nil
}
func (a researchReportStorageAdapter) VerifyImmutableUpload(ctx context.Context, key string) (researchrun.VerifiedReportObject, error) {
	uploads, ok := a.store.(storage.UploadSessionStorage)
	if !ok {
		return researchrun.VerifiedReportObject{}, fmt.Errorf("report upload storage unavailable")
	}
	object, err := uploads.VerifyUpload(ctx, key)
	if err != nil {
		return researchrun.VerifiedReportObject{}, err
	}
	return researchrun.VerifiedReportObject{Key: key, Generation: object.ChecksumSHA256, MediaType: object.ContentType, ContentHash: "sha256:" + object.ChecksumSHA256, ByteSize: object.SizeBytes}, nil
}
func (a researchReportStorageAdapter) ReadVerified(ctx context.Context, key, generation string) (io.ReadCloser, error) {
	object, err := a.VerifyImmutableUpload(ctx, key)
	if err != nil {
		return nil, err
	}
	if object.Generation != generation {
		return nil, fmt.Errorf("report generation changed")
	}
	return a.store.GetReader(ctx, key)
}
func (a researchReportStorageAdapter) PutImmutable(ctx context.Context, key string, data []byte, mediaType string) (researchrun.VerifiedReportObject, error) {
	expectedSum := sha256.Sum256(data)
	expectedHash := "sha256:" + hex.EncodeToString(expectedSum[:])
	if existing, err := a.VerifyImmutableUpload(ctx, key); err == nil {
		if existing.ContentHash != expectedHash || existing.ByteSize != int64(len(data)) {
			return researchrun.VerifiedReportObject{}, fmt.Errorf("immutable report key already contains different bytes")
		}
		return existing, nil
	}
	type immutableUploader interface {
		UploadImmutable(context.Context, string, []byte, string, string) (string, error)
	}
	var err error
	if uploader, ok := a.store.(immutableUploader); ok {
		_, err = uploader.UploadImmutable(ctx, key, data, mediaType, "")
	} else {
		_, err = a.store.Upload(ctx, key, data, mediaType, "")
	}
	if err != nil {
		return researchrun.VerifiedReportObject{}, err
	}
	object, err := a.VerifyImmutableUpload(ctx, key)
	if err != nil || object.ContentHash != expectedHash || object.ByteSize != int64(len(data)) {
		return researchrun.VerifiedReportObject{}, fmt.Errorf("immutable report write verification failed")
	}
	return object, nil
}
