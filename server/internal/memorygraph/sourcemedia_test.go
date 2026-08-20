package memorygraph

import (
	"archive/zip"
	"bytes"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type scriptedMediaResolver map[string][]byte

func (r scriptedMediaResolver) ResolveSourceBytes(sourceNode *Node) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r[sourceNode.AttachmentID])), nil
}

func appendMediaFile(t *testing.T, store *Store, attachmentID, mime string) (int, string) {
	t.Helper()
	seq, id, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID:     attachmentID,
		MIME:             mime,
		ExtractionStatus: ExtractionPending,
	})
	if err != nil {
		t.Fatalf("AppendSourceFile: %v", err)
	}
	return seq, id
}

func loadMedia(t *testing.T, store *Store, sourceID string, watermark int, data map[string][]byte, limits MediaLimits) MediaPayload {
	t.Helper()
	payload, err := (MediaLoader{Resolver: scriptedMediaResolver(data), Limits: limits}).Load(store, GraphView{AllowProject: true}, sourceID, watermark)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return payload
}

func TestSourceMediaTextHappyPath(t *testing.T) {
	store := newTestStore(t)
	_, id := appendMediaFile(t, store, "plain.txt", "text/plain")
	payload := loadMedia(t, store, id, 1, map[string][]byte{"plain.txt": []byte("hello media")}, DefaultMediaLimits())
	if payload.State != MediaEvidenceOK || payload.SniffedMIME != "text/plain; charset=utf-8" || string(payload.Text) != "hello media" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSourceMediaMIMESpoofIsDeniedAndAudited(t *testing.T) {
	store := newTestStore(t)
	_, id := appendMediaFile(t, store, "spoof.png", "image/png")
	payload := loadMedia(t, store, id, 1, map[string][]byte{"spoof.png": makeZip(t, map[string][]byte{"x.txt": []byte("x")})}, DefaultMediaLimits())
	if payload.State != MediaEvidenceDenied || !strings.Contains(payload.AuditDetail, "image/png") || !strings.Contains(payload.AuditDetail, "application/zip") {
		t.Fatalf("payload = %+v", payload)
	}
	audit, err := os.ReadFile(filepath.Join(store.Root, "shared", "sources", "audit.jsonl"))
	if err != nil || !strings.Contains(string(audit), "media_load") || !strings.Contains(string(audit), "denied") {
		t.Fatalf("audit = %q, err = %v", audit, err)
	}
}

func TestSourceMediaZipBombIsDecodedTruncated(t *testing.T) {
	store := newTestStore(t)
	_, id := appendMediaFile(t, store, "bomb.zip", "application/zip")
	limits := DefaultMediaLimits()
	limits.MaxDecodedBytes = 1 << 20
	payload := loadMedia(t, store, id, 1, map[string][]byte{"bomb.zip": makeZip(t, map[string][]byte{"zeros.txt": make([]byte, 10<<20)})}, limits)
	if payload.State != MediaEvidenceTruncated || !containsMediaField(payload.TruncatedFields, "decoded") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSourceMediaNestedArchiveIsRecursionTruncated(t *testing.T) {
	store := newTestStore(t)
	_, id := appendMediaFile(t, store, "nested.zip", "application/zip")
	inner := makeZip(t, map[string][]byte{"leaf.txt": []byte("leaf")})
	middle := makeZip(t, map[string][]byte{"inner.zip": inner})
	outer := makeZip(t, map[string][]byte{"middle.zip": middle})
	limits := DefaultMediaLimits()
	limits.MaxRecursionDepth = 2
	payload := loadMedia(t, store, id, 1, map[string][]byte{"nested.zip": outer}, limits)
	if payload.State != MediaEvidenceTruncated || !containsMediaField(payload.TruncatedFields, "recursion") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSourceMediaPixelOverflow(t *testing.T) {
	store := newTestStore(t)
	_, id := appendMediaFile(t, store, "large.png", "image/png")
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 200, 200))); err != nil {
		t.Fatal(err)
	}
	limits := DefaultMediaLimits()
	limits.MaxPixels = 100 * 100
	payload := loadMedia(t, store, id, 1, map[string][]byte{"large.png": imageData.Bytes()}, limits)
	if payload.State != MediaEvidenceTruncated || !containsMediaField(payload.TruncatedFields, "pixels") || payload.Width != 200 || payload.Height != 200 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSourceMediaContextCap(t *testing.T) {
	store := newTestStore(t)
	_, id := appendMediaFile(t, store, "long.txt", "text/plain")
	limits := DefaultMediaLimits()
	limits.MaxContextBytes = 5
	payload := loadMedia(t, store, id, 1, map[string][]byte{"long.txt": []byte("too much text")}, limits)
	if payload.State != MediaEvidenceTruncated || !containsMediaField(payload.TruncatedFields, "context") || len(payload.Text) != 5 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSourceMediaUnsupported(t *testing.T) {
	store := newTestStore(t)
	_, id := appendMediaFile(t, store, "binary.elf", "application/x-elf")
	payload := loadMedia(t, store, id, 1, map[string][]byte{"binary.elf": []byte{0x7f, 'E', 'L', 'F'}}, DefaultMediaLimits())
	if payload.State != MediaEvidenceUnsupported || len(payload.Text) != 0 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSourceMediaScopeKindAndWatermarkDenials(t *testing.T) {
	t.Run("scope", func(t *testing.T) {
		store := newTestStore(t)
		identity := stampGraphIdentity(t, store, GraphDirKindChannel)
		_, id := appendMediaFile(t, store, "channel.txt", "text/plain")
		payload, err := (MediaLoader{Resolver: scriptedMediaResolver{"channel.txt": []byte("secret")}, Limits: DefaultMediaLimits()}).Load(store, GraphView{AllowProject: true, ChannelID: identity.OwnerID + "-other"}, id, 1)
		if err != nil || payload.State != MediaEvidenceDenied {
			t.Fatalf("payload = %+v, err = %v", payload, err)
		}
	})
	t.Run("segment", func(t *testing.T) {
		store := newTestStore(t)
		_, err := store.AppendSourceSegment("segment", "body")
		if err != nil {
			t.Fatal(err)
		}
		payload, err := (MediaLoader{Resolver: scriptedMediaResolver{}, Limits: DefaultMediaLimits()}).Load(store, GraphView{AllowProject: true}, "segment", 1)
		if err != nil || payload.State != MediaEvidenceDenied {
			t.Fatalf("payload = %+v, err = %v", payload, err)
		}
	})
	t.Run("watermark", func(t *testing.T) {
		store := newTestStore(t)
		_, _ = appendMediaFile(t, store, "one.txt", "text/plain")
		_, id := appendMediaFile(t, store, "two.txt", "text/plain")
		payload := loadMedia(t, store, id, 1, map[string][]byte{"two.txt": []byte("late")}, DefaultMediaLimits())
		if payload.State != MediaEvidenceDenied {
			t.Fatalf("payload = %+v", payload)
		}
	})
}

func TestSourceMediaEmptyDeclaredMIMEUsesSniffedType(t *testing.T) {
	store := newTestStore(t)
	_, id := appendMediaFile(t, store, "unknown", "")
	payload := loadMedia(t, store, id, 1, map[string][]byte{"unknown": []byte("sniff this text")}, DefaultMediaLimits())
	if payload.State != MediaEvidenceOK || payload.SniffedMIME != "text/plain; charset=utf-8" || string(payload.Text) != "sniff this text" {
		t.Fatalf("payload = %+v", payload)
	}
}

func makeZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func containsMediaField(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}
