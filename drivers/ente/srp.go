package ente

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"math/bits"

	"golang.org/x/crypto/argon2"
)

// Password-login crypto, ported segment by segment from OpenList-APIPages
// frontend/src/lib/ente/crypto.ts (which mirrors the ente go-srp semantics
// the museum server actually runs).

// deriveKEK derives the key encryption key from the account password.
// time=opsLimit, memory=memLimit/1024 KiB, p=1, matching the ente clients.
func deriveKEK(password string, kekSalt []byte, opsLimit, memLimit int64) ([]byte, error) {
	if memLimit < 1024 || opsLimit < 1 {
		return nil, errors.New("ente: invalid argon2id limits")
	}
	return argon2.IDKey([]byte(password), kekSalt, uint32(opsLimit), uint32(memLimit/1024), 1, 32), nil
}

// ---------------------------------------------------------------------------
// BLAKE2b with key/salt/personalization
// ---------------------------------------------------------------------------

// blake2bParamSum computes BLAKE2b with a key, salt and personalization
// (libsodium crypto_generichash semantics). x/crypto/blake2b exposes none of
// the latter two, and the login key derivation needs all three.
func blake2bParamSum(size int, key, salt, personal, data []byte) ([]byte, error) {
	if size < 1 || size > 64 || len(key) > 64 || len(salt) != 16 || len(personal) != 16 {
		return nil, errors.New("ente: invalid blake2b parameters")
	}
	h := blake2bIV
	h[0] ^= uint64(size) | uint64(len(key))<<8 | 1<<16 | 1<<24 // fanout=1, depth=1
	h[4] ^= binary.LittleEndian.Uint64(salt[0:8])
	h[5] ^= binary.LittleEndian.Uint64(salt[8:16])
	h[6] ^= binary.LittleEndian.Uint64(personal[0:8])
	h[7] ^= binary.LittleEndian.Uint64(personal[8:16])

	var block [128]byte
	blockLen := 0
	ctr := uint64(0)
	compress := func(last bool) {
		ctr += uint64(blockLen)
		blake2bCompress(&h, ctr, block[:], last)
	}
	if len(key) > 0 {
		copy(block[:], key)
		blockLen = 128
	}
	// one block is always kept pending and closed by the final compress,
	// matching x/crypto: with no message data the key block is final
	for _, b := range data {
		if blockLen == 128 {
			compress(false)
			clear(block[:])
			blockLen = 0
		}
		block[blockLen] = b
		blockLen++
	}
	// BLAKE2 zero-pads the final block (no length marker)
	compress(true)

	out := make([]byte, size)
	for i := range size {
		out[i] = byte(h[i/8] >> (8 * (i % 8)))
	}
	return out, nil
}

var blake2bIV = [8]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}

// blake2bSigma is the round constants schedule from RFC 7693.
var blake2bSigma = [12][16]byte{
	{0, 2, 4, 6, 1, 3, 5, 7, 8, 10, 12, 14, 9, 11, 13, 15},
	{14, 4, 9, 13, 10, 8, 15, 6, 1, 0, 11, 5, 12, 2, 7, 3},
	{11, 12, 5, 15, 8, 0, 2, 13, 10, 3, 7, 9, 14, 6, 1, 4},
	{7, 3, 13, 11, 9, 1, 12, 14, 2, 5, 4, 15, 6, 10, 0, 8},
	{9, 5, 2, 10, 0, 7, 4, 15, 14, 11, 6, 3, 1, 12, 8, 13},
	{2, 6, 0, 8, 12, 10, 11, 3, 4, 7, 15, 1, 13, 5, 14, 9},
	{12, 1, 14, 4, 5, 15, 13, 10, 0, 6, 9, 8, 7, 3, 2, 11},
	{13, 7, 12, 3, 11, 14, 1, 9, 5, 15, 8, 2, 0, 4, 6, 10},
	{6, 14, 11, 0, 15, 9, 3, 8, 12, 13, 1, 10, 2, 7, 4, 5},
	{10, 8, 7, 1, 2, 4, 6, 5, 15, 9, 3, 13, 11, 14, 12, 0},
	{0, 2, 4, 6, 1, 3, 5, 7, 8, 10, 12, 14, 9, 11, 13, 15},
	{14, 4, 9, 13, 10, 8, 15, 6, 1, 0, 11, 5, 12, 2, 7, 3},
}

func blake2bG(v *[16]uint64, a, b, c, d int, x, y uint64) {
	v[a] += v[b] + x
	v[d] = bits.RotateLeft64(v[d]^v[a], -32)
	v[c] += v[d]
	v[b] = bits.RotateLeft64(v[b]^v[c], -24)
	v[a] += v[b] + y
	v[d] = bits.RotateLeft64(v[d]^v[a], -16)
	v[c] += v[d]
	v[b] = bits.RotateLeft64(v[b]^v[c], -63)
}

func blake2bCompress(h *[8]uint64, ctr uint64, block []byte, last bool) {
	var m [16]uint64
	for i := range m {
		m[i] = binary.LittleEndian.Uint64(block[i*8:])
	}
	var v [16]uint64
	copy(v[:8], h[:])
	copy(v[8:], blake2bIV[:])
	v[12] ^= ctr
	if last {
		v[14] = ^v[14]
	}
	for r := range 12 {
		s := &blake2bSigma[r]
		blake2bG(&v, 0, 4, 8, 12, m[s[0]], m[s[4]])
		blake2bG(&v, 1, 5, 9, 13, m[s[1]], m[s[5]])
		blake2bG(&v, 2, 6, 10, 14, m[s[2]], m[s[6]])
		blake2bG(&v, 3, 7, 11, 15, m[s[3]], m[s[7]])
		blake2bG(&v, 0, 5, 10, 15, m[s[8]], m[s[12]])
		blake2bG(&v, 1, 6, 11, 12, m[s[9]], m[s[13]])
		blake2bG(&v, 2, 7, 8, 13, m[s[10]], m[s[14]])
		blake2bG(&v, 3, 4, 9, 14, m[s[11]], m[s[15]])
	}
	for i := range h {
		h[i] ^= v[i] ^ v[i+8]
	}
}

// deriveLoginKey derives the 16-byte SRP password input from the KEK:
// blake2b(key=KEK, salt=LE64(1), personal="loginctx") truncated to 16 bytes.
func deriveLoginKey(kek []byte) ([]byte, error) {
	var salt, personal [16]byte
	binary.LittleEndian.PutUint64(salt[:8], 1)
	copy(personal[:], "loginctx")
	subKey, err := blake2bParamSum(32, kek, salt[:], personal[:], nil)
	if err != nil {
		return nil, err
	}
	return subKey[:16], nil
}

// ---------------------------------------------------------------------------
// SRP-6a, RFC 3526 group 4096, SHA-256
// ---------------------------------------------------------------------------

// RFC 3526 MODP group 4096.
const srpNHex = "FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74" +
	"020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F1437" +
	"4FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED" +
	"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF05" +
	"98DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB" +
	"9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B" +
	"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF695581718" +
	"3995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D04507A33" +
	"A85521ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7" +
	"ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6BF12FFA06D98A0864" +
	"D87602733EC86A64521F2B18177B200CBBE117577A615D6C770988C0BAD946E2" +
	"08E24FA074E5AB3143DB5BFCE0FD108E4B82D120A92108011A723C12A787E6D7" +
	"88719A10BDBA5B2699C327186AF4E23C1A946834B6150BDA2583E9CA2AD44CE8" +
	"DBBBC2DB04DE8EF92E8EFC141FBECAA6287C59474E6BC05D99B2964FA090C3A2" +
	"233BA186515BE7ED1F612970CEE2D7AFB81BDD762170481CD0069127D5B05AA9" +
	"93B4EA988D8FDDC186FFB7DC90A6C08F4DF435C934063199FFFFFFFFFFFFFFFF"

const (
	srpG       = 5
	srpKeySize = 512
)

var (
	srpN = mustBigFromHex(srpNHex)
	srpK = srpMultiplier()
)

func mustBigFromHex(hex string) *big.Int {
	n, ok := new(big.Int).SetString(hex, 16)
	if !ok {
		panic("ente: invalid SRP group constant")
	}
	return n
}

// srpMultiplier is k = H(pad(N) || pad(g)), ente go-srp semantics.
func srpMultiplier() *big.Int {
	return new(big.Int).SetBytes(srpHash(padSRP(srpN), padSRP(big.NewInt(srpG))))
}

// padSRP pads n to the 512-byte group representation (big-endian).
func padSRP(n *big.Int) []byte {
	out := make([]byte, srpKeySize)
	n.FillBytes(out)
	return out
}

func srpHash(inputs ...[]byte) []byte {
	h := sha256.New()
	for _, in := range inputs {
		h.Write(in)
	}
	return h.Sum(nil)
}

// srpSession is a client-side SRP-6a session. identity is the srpUserID
// bytes, salt the decoded srpSalt and password the derived login key.
type srpSession struct {
	a, x, A *big.Int
	m1, m2  []byte
}

// newSrpSession starts a session; secret1 (the private exponent a) is
// generated when nil.
func newSrpSession(identity, salt, password, secret1 []byte) (*srpSession, error) {
	if secret1 == nil {
		secret1 = make([]byte, 32)
		if _, err := rand.Read(secret1); err != nil {
			return nil, err
		}
	}
	s := &srpSession{a: new(big.Int).SetBytes(secret1)}
	// x = H(salt || H(identity || ':' || password))
	colon := []byte(":")
	s.x = new(big.Int).SetBytes(srpHash(salt, srpHash(identity, colon, password)))
	s.A = new(big.Int).Exp(big.NewInt(srpG), s.a, srpN)
	return s, nil
}

// computeA returns the 512-byte srpA; the server enforces that length.
func (s *srpSession) computeA() []byte {
	return padSRP(s.A)
}

// computeM1 derives M1 from the server srpB and caches M2 for verifyM2.
// All hash inputs use the group-padded representation, matching ente
// go-srp (kong/go-srp hashes minimal bytes and diverges ~1/256 logins).
func (s *srpSession) computeM1(srpB []byte) ([]byte, error) {
	B := new(big.Int).SetBytes(srpB)
	if B.Sign() <= 0 || B.Cmp(srpN) >= 0 {
		return nil, errors.New("ente: invalid server-supplied srpB")
	}
	aBytes := padSRP(s.A)
	bBytes := padSRP(B)
	u := new(big.Int).SetBytes(srpHash(aBytes, bBytes))
	// S = (B - k*g^x)^(a + u*x) mod N
	gx := new(big.Int).Exp(big.NewInt(srpG), s.x, srpN)
	base := new(big.Int).Mul(srpK, gx)
	base.Sub(B, base)
	base.Mod(base, srpN)
	if base.Sign() < 0 {
		base.Add(base, srpN)
	}
	e := new(big.Int).Mul(u, s.x)
	e.Add(e, s.a)
	sBytes := padSRP(new(big.Int).Exp(base, e, srpN))
	k := srpHash(sBytes)
	s.m1 = srpHash(aBytes, bBytes, sBytes)
	s.m2 = srpHash(aBytes, s.m1, k)
	return s.m1, nil
}

// verifyM2 checks the server srpM2 in constant time.
func (s *srpSession) verifyM2(srpM2 []byte) bool {
	if s.m2 == nil || len(srpM2) != len(s.m2) {
		return false
	}
	return subtle.ConstantTimeCompare(s.m2, srpM2) == 1
}

// ---------------------------------------------------------------------------
// keyAttributes / token decryption
// ---------------------------------------------------------------------------

// enteKeyAttributes is the keyAttributes object of the museum server.
type enteKeyAttributes struct {
	KEKSalt                  string `json:"kekSalt"`
	EncryptedKey             string `json:"encryptedKey"`
	KeyDecryptionNonce       string `json:"keyDecryptionNonce"`
	PublicKey                string `json:"publicKey"`
	EncryptedSecretKey       string `json:"encryptedSecretKey"`
	SecretKeyDecryptionNonce string `json:"secretKeyDecryptionNonce"`
	MemLimit                 int64  `json:"memLimit"`
	OpsLimit                 int64  `json:"opsLimit"`
}

// openKeyAttributes decrypts the master key with the KEK and the secret key
// with the master key.
func openKeyAttributes(attrs enteKeyAttributes, kek []byte) (masterKey, secretKey []byte, err error) {
	masterKey, err = secretboxOpenB64(attrs.EncryptedKey, attrs.KeyDecryptionNonce, kek)
	if err != nil {
		return nil, nil, fmt.Errorf("master key decryption failed (wrong password?): %w", err)
	}
	secretKey, err = secretboxOpenB64(attrs.EncryptedSecretKey, attrs.SecretKeyDecryptionNonce, masterKey)
	if err != nil {
		return nil, nil, fmt.Errorf("secret key decryption failed: %w", err)
	}
	return masterKey, secretKey, nil
}

// openToken unseals the encryptedToken sealed box into raw token bytes.
func openToken(encryptedTokenB64, publicKeyB64 string, secretKey []byte) ([]byte, error) {
	cipher, err := fromB64(encryptedTokenB64)
	if err != nil {
		return nil, err
	}
	publicKey, err := fromB64(publicKeyB64)
	if err != nil {
		return nil, err
	}
	return sealedBoxOpen(cipher, publicKey, secretKey)
}
