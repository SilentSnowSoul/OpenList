package ente

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	stdpath "path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"golang.org/x/crypto/curve25519"
)

// ---------------------------------------------------------------------------
// base64 / key helpers
// ---------------------------------------------------------------------------

// fromB64 decodes the base64 used by the museum server. Both the padded and the
// unpadded flavour are accepted, matching the leniency of the official clients.
func fromB64(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("ente: invalid base64: %w", err)
	}
	return b, nil
}

// secretboxOpenB64 opens a base64 secretbox (cipher + nonce) with key.
func secretboxOpenB64(cipherB64, nonceB64 string, key []byte) ([]byte, error) {
	cipher, err := fromB64(cipherB64)
	if err != nil {
		return nil, err
	}
	nonce, err := fromB64(nonceB64)
	if err != nil {
		return nil, err
	}
	return secretboxOpen(cipher, nonce, key)
}

// decryptMetadataB64 decrypts a single-chunk secretstream metadata blob.
func decryptMetadataB64(key []byte, headerB64, cipherB64 string) ([]byte, error) {
	header, err := fromB64(headerB64)
	if err != nil {
		return nil, err
	}
	cipher, err := fromB64(cipherB64)
	if err != nil {
		return nil, err
	}
	return decryptMetadata(key, header, cipher)
}

// boxPublicKey derives the Curve25519 public key of a secret key.
func boxPublicKey(secretKey []byte) ([]byte, error) {
	if len(secretKey) != 32 {
		return nil, errors.New("ente: secret key must be 32 bytes")
	}
	return curve25519.X25519(secretKey, curve25519.Basepoint)
}

// ---------------------------------------------------------------------------
// metadata JSON
// ---------------------------------------------------------------------------

// parseMetadataObject decrypts and JSON-decodes a metadata blob. Numbers are
// kept as json.Number so microsecond timestamps stay exact.
func parseMetadataObject(key []byte, headerB64, cipherB64 string) (map[string]any, error) {
	plain, err := decryptMetadataB64(key, headerB64, cipherB64)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(plain))
	dec.UseNumber()
	obj := map[string]any{}
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	return obj, nil
}

func jsonString(obj map[string]any, key string) string {
	s, _ := obj[key].(string)
	return s
}

func jsonInt64(obj map[string]any, key string) int64 {
	n, ok := obj[key].(json.Number)
	if !ok {
		return 0
	}
	if i, err := n.Int64(); err == nil {
		return i
	}
	if f, err := n.Float64(); err == nil {
		return int64(f)
	}
	return 0
}

// ---------------------------------------------------------------------------
// share URL
// ---------------------------------------------------------------------------

var sharePathRe = regexp.MustCompile(`/c/([^/?#]+)`)

var (
	errInvalidShareURL = errors.New("[EnteShare] invalid share URL")
	errInvalidShareKey = errors.New("[EnteShare] invalid collection key in share URL")
)

// ParseShareURL extracts the share token and the base58 collection key from a
// public share URL. Two shapes are accepted, both carrying the key only in the
// URL fragment, which browsers never send to the server:
// https://share.ente.io/c/<token>#<bs58 key> and
// https://albums.ente.com/?t=<token>#<bs58 key>.
func ParseShareURL(shareURL string) (string, []byte, error) {
	u, err := url.Parse(strings.TrimSpace(shareURL))
	if err != nil {
		return "", nil, errInvalidShareURL
	}
	token := ""
	if m := sharePathRe.FindStringSubmatch(u.Path); m != nil {
		token = m[1]
	} else {
		token = u.Query().Get("t")
	}
	if token == "" || u.Fragment == "" {
		return "", nil, errInvalidShareURL
	}
	key, err := bs58Decode(u.Fragment)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", errInvalidShareKey, err)
	}
	if len(key) != 32 {
		return "", nil, errInvalidShareKey
	}
	return token, key, nil
}

// ---------------------------------------------------------------------------
// decryption of diff entries
// ---------------------------------------------------------------------------

// EnteDecryptedFile is a diff entry with its metadata decrypted.
type EnteDecryptedFile struct {
	ID                        int64
	Name                      string
	Size                      int64
	ThumbSize                 int64
	ModifiedMs                int64
	FileKey                   []byte
	FileDecryptionHeader      string
	ThumbnailDecryptionHeader string
	HasThumb                  bool
}

// DecryptEnteFile decrypts the fileKey and metadata of one diff entry.
// pubMagicMetadata is optional: a failure there must not drop the file.
func DecryptEnteFile(f EnteDiffFile, collectionKey []byte) (*EnteDecryptedFile, error) {
	fileKey, err := secretboxOpenB64(f.EncryptedKey, f.KeyDecryptionNonce, collectionKey)
	if err != nil {
		return nil, err
	}
	meta := map[string]any{}
	if f.Metadata != nil && f.Metadata.EncryptedData != "" && f.Metadata.DecryptionHeader != "" {
		if meta, err = parseMetadataObject(fileKey, f.Metadata.DecryptionHeader, f.Metadata.EncryptedData); err != nil {
			return nil, err
		}
	}
	pubMagic := map[string]any{}
	if f.PubMagicMetadata != nil && f.PubMagicMetadata.Data != "" {
		if obj, err := parseMetadataObject(fileKey, f.PubMagicMetadata.Header, f.PubMagicMetadata.Data); err == nil {
			pubMagic = obj
		}
	}
	name := jsonString(pubMagic, "editedName")
	if name == "" {
		name = jsonString(meta, "title")
	}
	if name == "" {
		name = fmt.Sprintf("Ente-%d", f.ID)
	}
	timeUs := jsonInt64(pubMagic, "editedTime")
	if timeUs == 0 {
		timeUs = jsonInt64(meta, "creationTime")
	}
	if timeUs == 0 {
		timeUs = f.UpdationTime
	}
	var size, thumbSize int64
	if f.Info != nil {
		// Info.FileSize/ThumbSize are the encrypted object sizes; the
		// plaintext is smaller by the per-chunk secretstream overhead.
		size = decryptedSize(f.Info.FileSize)
		thumbSize = decryptedSize(f.Info.ThumbSize)
	}
	out := &EnteDecryptedFile{
		ID:         f.ID,
		Name:       strings.ReplaceAll(name, "/", "_"),
		Size:       size,
		ThumbSize:  thumbSize,
		ModifiedMs: timeUs / 1000,
		FileKey:    fileKey,
	}
	if f.File != nil {
		out.FileDecryptionHeader = f.File.DecryptionHeader
	}
	if f.Thumbnail != nil {
		out.ThumbnailDecryptionHeader = f.Thumbnail.DecryptionHeader
	}
	out.HasThumb = thumbSize > 0 && out.ThumbnailDecryptionHeader != ""
	return out, nil
}

// EnteDecryptedCollection is a collection with its key and name decrypted.
type EnteDecryptedCollection struct {
	ID         int64
	Key        []byte
	Name       string
	Hidden     bool
	ModifiedMs int64
}

var errSharedAlbumNeedsSecretKey = errors.New("ente: shared album requires secret_key")

// decryptEnteCollection decrypts a collection. Own albums carry a
// keyDecryptionNonce (masterKey secretbox); shared albums are sealed to the
// recipient's public key and need secretKey.
func decryptEnteCollection(c EnteCollection, masterKey, secretKey []byte) (*EnteDecryptedCollection, error) {
	var key []byte
	var err error
	switch {
	case c.KeyDecryptionNonce != "":
		key, err = secretboxOpenB64(c.EncryptedKey, c.KeyDecryptionNonce, masterKey)
	case len(secretKey) > 0:
		var cipher, pk []byte
		if cipher, err = fromB64(c.EncryptedKey); err == nil {
			if pk, err = boxPublicKey(secretKey); err == nil {
				key, err = sealedBoxOpen(cipher, pk, secretKey)
			}
		}
	default:
		return nil, errSharedAlbumNeedsSecretKey
	}
	if err != nil {
		return nil, err
	}
	plainName := c.Name
	if plainName == "" && c.EncryptedName != "" && c.NameDecryptionNonce != "" {
		dec, err := secretboxOpenB64(c.EncryptedName, c.NameDecryptionNonce, key)
		if err != nil {
			return nil, err
		}
		plainName = string(dec)
	}
	hidden := false
	if c.MagicMetadata != nil && c.MagicMetadata.Data != "" {
		if obj, err := parseMetadataObject(key, c.MagicMetadata.Header, c.MagicMetadata.Data); err == nil {
			hidden = jsonInt64(obj, "visibility") == 2
		}
	}
	if plainName == "" {
		plainName = fmt.Sprintf("Ente-%d", c.ID)
	}
	return &EnteDecryptedCollection{
		ID:         c.ID,
		Key:        key,
		Name:       strings.ReplaceAll(plainName, "/", "_"),
		Hidden:     hidden,
		ModifiedMs: c.UpdationTime / 1000,
	}, nil
}

// ---------------------------------------------------------------------------
// diff pagination
// ---------------------------------------------------------------------------

// PaginateEnteDiff walks the diff pages until the watermark stops advancing.
// Entries are keyed by ID (last write wins), deletions remove entries, and
// tombstones (encryptedData == "-") are dropped. The result is stably ordered
// by updationTime then id.
func PaginateEnteDiff(ctx context.Context, fetch func(ctx context.Context, sinceTime int64) (*EnteDiffResponse, error)) ([]EnteDiffFile, error) {
	byID := make(map[int64]EnteDiffFile)
	var sinceTime int64
	hasMore := true
	for hasMore {
		prev := sinceTime
		page, err := fetch(ctx, sinceTime)
		if err != nil {
			return nil, err
		}
		for _, f := range page.Diff {
			if f.UpdationTime > sinceTime {
				sinceTime = f.UpdationTime
			}
			if f.IsDeleted {
				delete(byID, f.ID)
			} else {
				byID[f.ID] = f
			}
		}
		hasMore = page.HasMore
		if hasMore && sinceTime == prev {
			break
		}
	}
	out := make([]EnteDiffFile, 0, len(byID))
	for _, f := range byID {
		if f.File != nil && f.File.EncryptedData == "-" {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdationTime != out[j].UpdationTime {
			return out[i].UpdationTime < out[j].UpdationTime
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// ---------------------------------------------------------------------------
// name dedupe
// ---------------------------------------------------------------------------

// DedupeNames appends a deterministic " (2)" suffix (before the extension) to
// duplicate names. Items must already be in a stable order.
func DedupeNames[T any](items []T, name func(*T) *string) {
	seen := make(map[string]int, len(items))
	for i := range items {
		cur := name(&items[i])
		n := seen[*cur]
		seen[*cur] = n + 1
		if n > 0 {
			*cur = addDedupeSuffix(*cur, n+1)
		}
	}
}

func addDedupeSuffix(name string, n int) string {
	if dot := strings.LastIndex(name, "."); dot > 0 {
		return fmt.Sprintf("%s (%d)%s", name[:dot], n, name[dot:])
	}
	return fmt.Sprintf("%s (%d)", name, n)
}

// ---------------------------------------------------------------------------
// secretstream download
// ---------------------------------------------------------------------------

// cipherChunkSize is the ciphertext size of a full secretstream chunk:
// 4 MiB of plaintext plus the tag byte and the Poly1305 MAC.
const cipherChunkSize = 4*1024*1024 + abYTES

// decryptedSize converts an encrypted object size to its plaintext size by
// subtracting the per-chunk secretstream overhead.
func decryptedSize(encryptedSize int64) int64 {
	if encryptedSize <= 0 {
		return 0
	}
	chunks := (encryptedSize + cipherChunkSize - 1) / cipherChunkSize
	return encryptedSize - chunks*abYTES
}

// enteStreamReader decrypts a secretstream sequentially. It buffers ciphertext
// until a full chunk is available, so the plaintext does not depend on network
// chunk boundaries; the trailing partial chunk is flushed once the source ends.
type enteStreamReader struct {
	src      io.ReadCloser
	dec      *streamDecryptor
	pending  []byte
	readBuf  []byte
	plain    []byte
	plainPos int
	done     bool
	err      error
}

func newEnteStreamReader(src io.ReadCloser, key []byte, header string) (*enteStreamReader, error) {
	hdr, err := fromB64(header)
	if err != nil {
		return nil, err
	}
	dec, err := newStreamDecryptor(key, hdr)
	if err != nil {
		return nil, err
	}
	return &enteStreamReader{src: src, dec: dec}, nil
}

func (r *enteStreamReader) Read(p []byte) (int, error) {
	for {
		if r.plainPos < len(r.plain) {
			n := copy(p, r.plain[r.plainPos:])
			r.plainPos += n
			return n, nil
		}
		if r.err != nil {
			return 0, r.err
		}
		if r.done {
			return 0, io.EOF
		}
		if err := r.fill(); err != nil {
			r.err = err
			return 0, err
		}
	}
}

func (r *enteStreamReader) fill() error {
	for len(r.pending) < cipherChunkSize {
		if r.readBuf == nil {
			r.readBuf = make([]byte, 64*1024)
		}
		n, err := r.src.Read(r.readBuf)
		if n > 0 {
			r.pending = append(r.pending, r.readBuf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if len(r.pending) == 0 {
		return errors.New("[Ente] stream ended before final tag")
	}
	take := len(r.pending)
	if take > cipherChunkSize {
		take = cipherChunkSize
	}
	plain, tag, err := r.dec.pull(r.pending[:take])
	if err != nil {
		return err
	}
	r.pending = r.pending[take:]
	r.plain = plain
	r.plainPos = 0
	if tag == tagFinal {
		r.done = true
	}
	return nil
}

func (r *enteStreamReader) Close() error {
	return r.src.Close()
}

// limitedReadCloser bounds a reader and closes the underlying stream.
type limitedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (l *limitedReadCloser) Close() error { return l.closer.Close() }

// openDecryptedStream fetches the presigned URL and returns a reader over the
// decrypted plaintext, positioned at r.Start. Ente's secretstream is
// sequential, so a non-zero start is served by decrypting from the head and
// discarding the prefix: seek cost grows with the offset (v1 does no
// block-level random access).
func openDecryptedStream(ctx context.Context, client *Client, fetchURL func(context.Context) (string, error), key []byte, header string, r http_range.Range) (io.ReadCloser, error) {
	u, err := fetchURL(ctx)
	if err != nil {
		return nil, err
	}
	body, err := client.FetchBinary(ctx, u)
	if err != nil {
		return nil, err
	}
	sr, err := newEnteStreamReader(body, key, header)
	if err != nil {
		body.Close()
		return nil, err
	}
	if r.Start > 0 {
		if _, err := io.CopyN(io.Discard, sr, r.Start); err != nil {
			sr.Close()
			return nil, fmt.Errorf("ente: cannot seek to %d: %w", r.Start, err)
		}
	}
	if r.Length >= 0 {
		return &limitedReadCloser{Reader: io.LimitReader(sr, r.Length), closer: sr}, nil
	}
	return sr, nil
}

// NewRangeReader builds a RangeReader that streams the decrypted plaintext of
// one file. The returned function is safe to call once per range request.
func NewRangeReader(client *Client, fetchURL func(context.Context) (string, error), key []byte, header string) stream.RangeReaderFunc {
	return func(ctx context.Context, r http_range.Range) (io.ReadCloser, error) {
		return openDecryptedStream(ctx, client, fetchURL, key, header, r)
	}
}

// ---------------------------------------------------------------------------
// helpers shared by the Ente and EnteShare drivers
// ---------------------------------------------------------------------------

// ThumbURL builds the local proxy URL for a thumbnail, following the local
// driver convention: /p/<mount>/<dirPath>/<name>?type=thumb&sign=<sign>. The
// signed path must equal what middlewares.PathParse recovers from the URL, so
// it is derived from the object's own path rather than args.ReqPath (which is
// empty for driver.Get).
func ThumbURL(ctx context.Context, mountPath, dirPath, name string) string {
	p := stdpath.Join(mountPath, dirPath, name)
	u := common.GetApiUrl(ctx) + stdpath.Join("/p", p)
	u = utils.EncodePath(u, true)
	return u + "?type=thumb&sign=" + sign.Sign(p)
}

// RootObj returns the synthetic root object of an Ente mount.
func RootObj(rootPath string, modified time.Time) model.Obj {
	return &model.Object{
		Path:     rootPath,
		Name:     "root",
		Modified: modified,
		Mask:     model.Locked,
		IsFolder: true,
	}
}
