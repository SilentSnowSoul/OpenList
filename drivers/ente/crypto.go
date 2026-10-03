package ente

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/nacl/secretbox"
	"golang.org/x/crypto/poly1305"
)

const (
	// abYTES is the per-chunk overhead of the Ente secretstream:
	// 1 encrypted tag byte + 16 Poly1305 MAC bytes.
	abYTES = 17
	// Chunk tags (libsodium secretstream compatible).
	tagMessage = 0
	tagPush    = 1
	tagRekey   = 2
	tagFinal   = tagPush | tagRekey
)

// secretboxOpen opens an XSalsa20-Poly1305 secretbox, used for the
// collectionKey layer (own albums) and the fileKey layer (metadata).
func secretboxOpen(cipher, nonce, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("ente: secretbox key must be 32 bytes")
	}
	if len(nonce) != 24 {
		return nil, errors.New("ente: secretbox nonce must be 24 bytes")
	}
	var n [24]byte
	var k [32]byte
	copy(n[:], nonce)
	copy(k[:], key)
	plain, ok := secretbox.Open(nil, cipher, &n, &k)
	if !ok {
		return nil, errors.New("ente: secretbox open failed")
	}
	return plain, nil
}

// sealedBoxOpen opens a Curve25519 sealed box, used for the collectionKey
// layer of shared albums. The nonce is blake2b-24(epk ‖ recipientPK),
// matching the Go box implementation (not libsodium's sha256).
func sealedBoxOpen(cipher, publicKey, secretKey []byte) ([]byte, error) {
	if len(cipher) < 48 {
		return nil, errors.New("ente: sealed box too short")
	}
	if len(publicKey) != 32 || len(secretKey) != 32 {
		return nil, errors.New("ente: sealed box keys must be 32 bytes")
	}
	h, err := blake2b.New(24, nil)
	if err != nil {
		return nil, err
	}
	h.Write(cipher[:32]) // ephemeral public key
	h.Write(publicKey)
	nonce := h.Sum(nil)
	var epk, pk, sk [32]byte
	var n [24]byte
	copy(epk[:], cipher[:32])
	copy(pk[:], publicKey)
	copy(sk[:], secretKey)
	copy(n[:], nonce)
	plain, ok := box.Open(nil, cipher[32:], &n, &epk, &sk)
	if !ok {
		return nil, errors.New("ente: sealed box open failed")
	}
	return plain, nil
}

// streamDecryptor decrypts Ente's custom XChaCha20 secretstream
// (a Go port of libsodium secretstream, as used by the ente CLI).
//
// Per-chunk ciphertext layout: [tag^ks1[0]] ‖ [plaintext^ks(128..)] ‖ [mac16]
// where ks is the ChaCha20 keystream for the chunk nonce and ks1 the first
// 128 keystream bytes. The MAC input is
//
//	[encTag ‖ ks1[65:128]] ‖ cipher[1:1+mlen] ‖ pad(mlen&15) ‖ le64(0) ‖ le64(64+mlen)
type streamDecryptor struct {
	key   [32]byte
	nonce [12]byte
}

func newStreamDecryptor(key, header []byte) (*streamDecryptor, error) {
	if len(key) != 32 {
		return nil, errors.New("ente: stream key must be 32 bytes")
	}
	if len(header) != 24 {
		return nil, errors.New("ente: stream header must be 24 bytes")
	}
	sub, err := chacha20.HChaCha20(key, header[:16])
	if err != nil {
		return nil, err
	}
	d := &streamDecryptor{}
	copy(d.key[:], sub)
	d.nonce[0] = 1
	copy(d.nonce[4:], header[16:24])
	return d, nil
}

// pull decrypts exactly one chunk and advances the stream state.
// Chunks must be fed in order; out-of-order or tampered chunks fail the MAC.
func (d *streamDecryptor) pull(cipher []byte) (plain []byte, tag byte, err error) {
	if len(cipher) < abYTES {
		return nil, 0, errors.New("ente: ciphertext too short")
	}
	mlen := len(cipher) - abYTES
	// block01: first 128 keystream bytes (blocks 0 and 1), used for the
	// Poly1305 key, the tag key byte and the MAC prefix.
	block01 := make([]byte, 128)
	cs0, err := chacha20.NewUnauthenticatedCipher(d.key[:], d.nonce[:])
	if err != nil {
		return nil, 0, err
	}
	cs0.XORKeyStream(block01, block01)

	var macKey [32]byte
	copy(macKey[:], block01[:32])
	mac := poly1305.New(&macKey)
	var macBlock [64]byte
	macBlock[0] = cipher[0]
	copy(macBlock[1:], block01[65:128])
	mac.Write(macBlock[:])
	mac.Write(cipher[1 : 1+mlen])
	if mlen&15 != 0 {
		mac.Write(make([]byte, mlen&15))
	}
	var zero [8]byte
	mac.Write(zero[:])
	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], uint64(64+mlen))
	mac.Write(lenBuf[:])
	digest := mac.Sum(nil)
	if !bytes.Equal(digest, cipher[1+mlen:]) {
		return nil, 0, errors.New("ente: MAC verification failed")
	}
	tag = cipher[0] ^ block01[64]
	// Plaintext keystream starts at block 2 (counter 2): the first two
	// blocks are consumed by the tag/MAC construction above.
	cs, err := chacha20.NewUnauthenticatedCipher(d.key[:], d.nonce[:])
	if err != nil {
		return nil, 0, err
	}
	var skip [128]byte
	cs.XORKeyStream(skip[:], skip[:])
	plain = make([]byte, mlen)
	cs.XORKeyStream(plain, cipher[1:1+mlen])
	// Nonce advance: nonce[4:12] ^= mac; nonce[0:4] little-endian +1 (with carry).
	for i := 4; i < 12; i++ {
		d.nonce[i] ^= digest[i-4]
	}
	c := 1
	for i := 0; i < 4; i++ {
		c += int(d.nonce[i])
		d.nonce[i] = byte(c)
		c >>= 8
	}
	return plain, tag, nil
}

// decryptMetadata decrypts a single-chunk secretstream (file/collection
// metadata); the chunk tag must be tagFinal.
func decryptMetadata(key, header, cipher []byte) ([]byte, error) {
	d, err := newStreamDecryptor(key, header)
	if err != nil {
		return nil, err
	}
	plain, tag, err := d.pull(cipher)
	if err != nil {
		return nil, err
	}
	if tag != tagFinal {
		return nil, errors.New("ente: metadata tag is not final")
	}
	return plain, nil
}

const bs58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var (
	bs58Base   = big.NewInt(58)
	bs58Single = new(big.Int)
)

// bs58Decode decodes a base58 string (Bitcoin alphabet) using math/big.
// Decode only; leading '1's encode leading zero bytes.
func bs58Decode(s string) ([]byte, error) {
	num := new(big.Int)
	for _, c := range s {
		v := strings.IndexRune(bs58Alphabet, c)
		if v < 0 {
			return nil, fmt.Errorf("ente: invalid base58 character: %c", c)
		}
		num.Mul(num, bs58Base)
		num.Add(num, bs58Single.SetInt64(int64(v)))
	}
	decoded := num.Bytes()
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	out := make([]byte, zeros+len(decoded))
	copy(out[zeros:], decoded)
	return out, nil
}
