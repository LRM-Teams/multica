package memorygraph

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
)

// MediaLimits bounds a source-media load. Duration is reserved for formats
// whose stdlib decoder exposes duration metadata; no supported decoder does.
type MediaLimits struct {
	MaxCompressedBytes int64
	MaxDecodedBytes    int64
	MaxRecursionDepth  int64
	MaxPages           int64
	MaxDurationSec     int64
	MaxPixels          int64
	MaxContextBytes    int64
}

// DefaultMediaLimits returns conservative limits for server-owned media loads.
func DefaultMediaLimits() MediaLimits {
	return MediaLimits{
		MaxCompressedBytes: 32 << 20,
		MaxDecodedBytes:    256 << 20,
		MaxRecursionDepth:  3,
		MaxPages:           200,
		MaxDurationSec:     300,
		MaxPixels:          40_000_000,
		MaxContextBytes:    1 << 20,
	}
}

// MediaEvidenceState records whether media can supply extraction evidence.
type MediaEvidenceState string

const (
	MediaEvidenceOK          MediaEvidenceState = "ok"
	MediaEvidenceTruncated   MediaEvidenceState = "truncated"
	MediaEvidenceUnsupported MediaEvidenceState = "unsupported"
	MediaEvidenceDenied      MediaEvidenceState = "denied"
)

// MediaPayload is the bounded evidence available to an extraction record.
type MediaPayload struct {
	State           MediaEvidenceState
	SniffedMIME     string
	DeclaredMIME    string
	Text            []byte
	Pages           int64
	DurationSec     int64
	Width           int
	Height          int
	TruncatedFields []string
	AuditDetail     string
}

// MediaByteResolver is the only source of source-media bytes. The loader
// passes it a validated, published source node and never constructs a path or URL.
type MediaByteResolver interface {
	ResolveSourceBytes(sourceNode *Node) (io.ReadCloser, error)
}

// MediaLoader reads source-owned attachment bytes under MediaLimits.
type MediaLoader struct {
	Resolver MediaByteResolver
	Limits   MediaLimits
}

// Load validates sourceID against store ownership before reading any bytes.
func (l MediaLoader) Load(store *Store, view GraphView, sourceID string, watermark int) (MediaPayload, error) {
	if store == nil {
		return MediaPayload{}, fmt.Errorf("media load: nil store")
	}
	if l.Resolver == nil {
		return MediaPayload{}, fmt.Errorf("media load: nil resolver")
	}
	limits := normalizedMediaLimits(l.Limits)

	source, seq, published, err := storeMediaSource(store, sourceID)
	if err != nil {
		return MediaPayload{}, err
	}
	if source == nil || !published || source.Level != SourceLayerLevel {
		return l.denied(store, sourceID, "source is not a published level -1 source")
	}
	if !view.Allows(source) {
		return l.denied(store, source.NodeID, "source is outside graph view")
	}
	if seq > watermark {
		return l.denied(store, source.NodeID, fmt.Sprintf("source seq %d exceeds watermark %d", seq, watermark))
	}
	if source.SourceKind != SourceKindFile {
		return l.denied(store, source.NodeID, "source has no attachment media")
	}
	if source.AttachmentID == "" {
		return MediaPayload{}, fmt.Errorf("media load: file source %s has no attachment id", source.NodeID)
	}

	payload := MediaPayload{DeclaredMIME: source.MIME}
	declared := mediaMIME(source.MIME)
	if declared != "" && !supportedMediaMIME(declared) {
		payload.State = MediaEvidenceUnsupported
		payload.AuditDetail = "media load state=unsupported declared_mime=" + declared
		return payload, nil
	}

	body, err := l.Resolver.ResolveSourceBytes(source)
	if err != nil {
		return MediaPayload{}, fmt.Errorf("media load resolve %s: %w", source.NodeID, err)
	}
	if body == nil {
		return MediaPayload{}, fmt.Errorf("media load resolve %s: nil reader", source.NodeID)
	}
	defer body.Close()

	raw, compressedOverflow, err := readMediaCompressed(body, limits.MaxCompressedBytes)
	if err != nil {
		return MediaPayload{}, fmt.Errorf("media load read %s: %w", source.NodeID, err)
	}
	payload.SniffedMIME = http.DetectContentType(raw[:minMedia(len(raw), 512)])
	sniffed := mediaMIME(payload.SniffedMIME)
	if mimeSpoofed(declared, sniffed, raw) {
		return l.denied(store, source.NodeID, fmt.Sprintf("declared_mime=%s sniffed_mime=%s", declared, sniffed))
	}

	effective := canonicalMediaMIME(sniffed)
	if effective == "" {
		effective = declared
	}
	if !supportedMediaMIME(effective) {
		payload.State = MediaEvidenceUnsupported
		payload.AuditDetail = "media load state=unsupported sniffed_mime=" + sniffed
		return payload, nil
	}
	if compressedOverflow {
		if mediaTextMIME(effective) {
			appendMediaText(&payload, raw, limits.MaxContextBytes)
		}
		addMediaTruncation(&payload, "compressed")
		return l.finish(store, source.NodeID, payload)
	}

	switch effective {
	case "application/zip", "application/gzip":
		state := mediaArchiveState{payload: &payload, limits: limits}
		if err := state.extract(raw, effective, 1); err != nil {
			return MediaPayload{}, err
		}
	case "application/pdf":
		// PDF page counting intentionally uses /Type /Page markers as a bounded heuristic.
		payload.Pages = int64(bytes.Count(raw, []byte("/Type /Page")))
		if payload.Pages > limits.MaxPages {
			addMediaTruncation(&payload, "pages")
		}
	case "image/png", "image/jpeg":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return MediaPayload{}, fmt.Errorf("media load decode image %s: %w", source.NodeID, err)
		}
		payload.Width, payload.Height = cfg.Width, cfg.Height
		if int64(cfg.Width)*int64(cfg.Height) > limits.MaxPixels {
			addMediaTruncation(&payload, "pixels")
		}
	default:
		appendMediaText(&payload, raw, limits.MaxContextBytes)
	}
	return l.finish(store, source.NodeID, payload)
}

func storeMediaSource(store *Store, sourceID string) (*Node, int, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	node, err := store.loadSourceNodeLocked(sourceID)
	if err != nil {
		return nil, 0, false, fmt.Errorf("media load source: %w", err)
	}
	if node == nil {
		return nil, 0, false, nil
	}
	published, err := store.sourcePublishedLocked(sourceID)
	if err != nil {
		return nil, 0, false, fmt.Errorf("media load publication: %w", err)
	}
	seq, err := store.seqForSourceLocked(sourceID)
	if err != nil {
		return nil, 0, false, fmt.Errorf("media load seq: %w", err)
	}
	return node, seq, published, nil
}

func (l MediaLoader) denied(store *Store, sourceID, detail string) (MediaPayload, error) {
	payload := MediaPayload{State: MediaEvidenceDenied, AuditDetail: "media load state=denied " + detail}
	return l.finish(store, sourceID, payload)
}

func (l MediaLoader) finish(store *Store, sourceID string, payload MediaPayload) (MediaPayload, error) {
	if len(payload.TruncatedFields) > 0 {
		payload.State = MediaEvidenceTruncated
		payload.AuditDetail = "media load state=truncated fields=" + strings.Join(payload.TruncatedFields, ",")
	} else if payload.State == "" {
		payload.State = MediaEvidenceOK
		payload.AuditDetail = "media load state=ok"
	}
	if payload.State == MediaEvidenceDenied || payload.State == MediaEvidenceTruncated {
		entry := SourceAuditFinding{Kind: "media_load", SourceID: sourceID, Detail: payload.AuditDetail}
		if err := appendJSONL(store.sourceAuditPath(), entry); err != nil {
			return MediaPayload{}, fmt.Errorf("media load audit: %w", err)
		}
	}
	return payload, nil
}

func normalizedMediaLimits(limits MediaLimits) MediaLimits {
	defaults := DefaultMediaLimits()
	if limits.MaxCompressedBytes <= 0 {
		limits.MaxCompressedBytes = defaults.MaxCompressedBytes
	}
	if limits.MaxDecodedBytes <= 0 {
		limits.MaxDecodedBytes = defaults.MaxDecodedBytes
	}
	if limits.MaxRecursionDepth <= 0 {
		limits.MaxRecursionDepth = defaults.MaxRecursionDepth
	}
	if limits.MaxPages <= 0 {
		limits.MaxPages = defaults.MaxPages
	}
	if limits.MaxDurationSec <= 0 {
		limits.MaxDurationSec = defaults.MaxDurationSec
	}
	if limits.MaxPixels <= 0 {
		limits.MaxPixels = defaults.MaxPixels
	}
	if limits.MaxContextBytes <= 0 {
		limits.MaxContextBytes = defaults.MaxContextBytes
	}
	return limits
}

func readMediaCompressed(r io.Reader, limit int64) ([]byte, bool, error) {
	if int64(int(limit)) != limit {
		return nil, false, fmt.Errorf("compressed limit is too large")
	}
	data := make([]byte, 0, int(limit))
	buf := make([]byte, 32*1024)
	for int64(len(data)) < limit {
		remaining := int(limit - int64(len(data)))
		if remaining < len(buf) {
			buf = buf[:remaining]
		}
		n, err := r.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if err == io.EOF {
			return data, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if n == 0 {
			return nil, false, io.ErrNoProgress
		}
	}
	var extra [1]byte
	n, err := r.Read(extra[:])
	if n > 0 {
		return data, true, nil
	}
	if err == io.EOF {
		return data, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return nil, false, io.ErrNoProgress
}

func mediaMIME(raw string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(raw, ";")[0]))
}

func canonicalMediaMIME(mime string) string {
	if mime == "application/x-gzip" {
		return "application/gzip"
	}
	return mime
}

func supportedMediaMIME(mime string) bool {
	return mediaTextMIME(mime) || mime == "application/pdf" || mime == "image/png" || mime == "image/jpeg" || mime == "application/zip" || mime == "application/gzip" || mime == "application/x-gzip"
}

func mediaTextMIME(mime string) bool {
	return strings.HasPrefix(mime, "text/") || mime == "application/json"
}

func mimeSpoofed(declared, sniffed string, raw []byte) bool {
	if declared == "application/pdf" {
		return !bytes.HasPrefix(raw, []byte("%PDF"))
	}
	if strings.HasPrefix(declared, "image/") || strings.HasPrefix(declared, "text/") {
		return sniffed == "application/octet-stream" || mimeFamily(declared) != mimeFamily(sniffed)
	}
	return false
}

func mimeFamily(mime string) string {
	before, _, ok := strings.Cut(mime, "/")
	if !ok {
		return mime
	}
	return before
}

func appendMediaText(payload *MediaPayload, data []byte, limit int64) {
	remaining := limit - int64(len(payload.Text))
	if remaining <= 0 {
		if len(data) > 0 {
			addMediaTruncation(payload, "context")
		}
		return
	}
	if int64(len(data)) > remaining {
		payload.Text = append(payload.Text, data[:remaining]...)
		addMediaTruncation(payload, "context")
		return
	}
	payload.Text = append(payload.Text, data...)
}

func addMediaTruncation(payload *MediaPayload, field string) {
	for _, existing := range payload.TruncatedFields {
		if existing == field {
			return
		}
	}
	payload.TruncatedFields = append(payload.TruncatedFields, field)
}

func mediaArchiveStopped(payload *MediaPayload) bool {
	for _, field := range payload.TruncatedFields {
		if field == "decoded" || field == "recursion" {
			return true
		}
	}
	return false
}

type mediaArchiveState struct {
	payload *MediaPayload
	limits  MediaLimits
	decoded int64
}

func (s *mediaArchiveState) extract(data []byte, mime string, depth int64) error {
	if depth > s.limits.MaxRecursionDepth {
		addMediaTruncation(s.payload, "recursion")
		return nil
	}
	switch mime {
	case "application/zip":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return fmt.Errorf("media load open zip: %w", err)
		}
		for _, file := range zr.File {
			if file.FileInfo().IsDir() || mediaArchiveStopped(s.payload) {
				continue
			}
			r, err := file.Open()
			if err != nil {
				return fmt.Errorf("media load open zip member %s: %w", file.Name, err)
			}
			err = s.extractMember(r, depth)
			closeErr := r.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return fmt.Errorf("media load close zip member %s: %w", file.Name, closeErr)
			}
		}
	case "application/gzip":
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("media load open gzip: %w", err)
		}
		err = s.extractMember(zr, depth)
		closeErr := zr.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("media load close gzip: %w", closeErr)
		}
	}
	return nil
}

func (s *mediaArchiveState) extractMember(r io.Reader, depth int64) error {
	var prefix []byte
	var archive []byte
	mode := "unknown"
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if s.decoded+int64(n) > s.limits.MaxDecodedBytes {
				allowed := s.limits.MaxDecodedBytes - s.decoded
				if allowed > 0 {
					s.consumeMemberChunk(&prefix, &archive, &mode, buf[:allowed])
				}
				s.decoded = s.limits.MaxDecodedBytes
				addMediaTruncation(s.payload, "decoded")
				return nil
			}
			s.decoded += int64(n)
			s.consumeMemberChunk(&prefix, &archive, &mode, buf[:n])
			if mode == "archive" && depth+1 > s.limits.MaxRecursionDepth {
				addMediaTruncation(s.payload, "recursion")
				return nil
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("media load decompress: %w", err)
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	if mode == "unknown" {
		s.selectMemberMode(&prefix, &archive, &mode)
	}
	if mode == "archive" {
		mime := "application/zip"
		if gzipMagic(archive) {
			mime = "application/gzip"
		}
		if depth+1 > s.limits.MaxRecursionDepth {
			addMediaTruncation(s.payload, "recursion")
			return nil
		}
		return s.extract(archive, mime, depth+1)
	}
	return nil
}

func (s *mediaArchiveState) consumeMemberChunk(prefix, archive *[]byte, mode *string, chunk []byte) {
	if *mode == "unknown" {
		need := 512 - len(*prefix)
		if need > len(chunk) {
			need = len(chunk)
		}
		*prefix = append(*prefix, chunk[:need]...)
		if len(*prefix) < 4 && need == len(chunk) {
			return
		}
		s.selectMemberMode(prefix, archive, mode)
		chunk = chunk[need:]
	}
	switch *mode {
	case "archive":
		*archive = append(*archive, chunk...)
	case "text":
		appendMediaText(s.payload, chunk, s.limits.MaxContextBytes)
	}
}

func (s *mediaArchiveState) selectMemberMode(prefix, archive *[]byte, mode *string) {
	if zipMagic(*prefix) || gzipMagic(*prefix) {
		*mode = "archive"
		*archive = append(*archive, (*prefix)...)
		return
	}
	if mediaTextMIME(mediaMIME(http.DetectContentType(*prefix))) {
		*mode = "text"
		appendMediaText(s.payload, *prefix, s.limits.MaxContextBytes)
		return
	}
	*mode = "binary"
}

func zipMagic(data []byte) bool {
	return len(data) >= 4 && bytes.Equal(data[:4], []byte("PK\x03\x04"))
}

func gzipMagic(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

func minMedia(a, b int) int {
	if a < b {
		return a
	}
	return b
}
