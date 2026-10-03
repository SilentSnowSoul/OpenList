package ente

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/glebarez/sqlite"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/nacl/secretbox"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// SRP account fixture
// ---------------------------------------------------------------------------

const (
	testEmail     = "user@example.com"
	testPassword  = "correct horse battery staple"
	test2FASecret = "JBSWY3DPEHPK3PXP"
)

// srpAccount is a full server-side account: the SRP verifier derived from the
// password plus the keyAttributes and sealed token handed out on success.
type srpAccount struct {
	email       string
	srpUserID   string
	kekSalt     []byte
	srpSalt     []byte
	v           *big.Int // verifier g^x
	masterKey   []byte
	secretKey   []byte
	keyAttrs    enteKeyAttributes
	tokenRaw    []byte
	tokenB64    string
	encTokenB64 string
}

func b64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func newSrpAccount(t *testing.T, email, password string) *srpAccount {
	t.Helper()
	mustRand := func(n int) []byte {
		b := make([]byte, n)
		rand.Read(b)
		return b
	}
	acc := &srpAccount{
		email:     email,
		srpUserID: "17001",
		kekSalt:   mustRand(16),
		srpSalt:   mustRand(16),
		masterKey: mustRand(32),
		secretKey: mustRand(32),
		tokenRaw:  mustRand(32),
	}
	acc.tokenB64 = base64.URLEncoding.EncodeToString(acc.tokenRaw)
	kek, err := deriveKEK(password, acc.kekSalt, 3, 8192)
	if err != nil {
		t.Fatal(err)
	}
	loginKey, err := deriveLoginKey(kek)
	if err != nil {
		t.Fatal(err)
	}
	x := new(big.Int).SetBytes(srpHash(acc.srpSalt, srpHash([]byte(acc.srpUserID), []byte(":"), loginKey)))
	acc.v = new(big.Int).Exp(big.NewInt(srpG), x, srpN)

	encKey, keyNonce := sealB64(acc.masterKey, kek)
	encSecret, secretNonce := sealB64(acc.secretKey, acc.masterKey)
	pk, err := boxPublicKey(acc.secretKey)
	if err != nil {
		t.Fatal(err)
	}
	acc.keyAttrs = enteKeyAttributes{
		KEKSalt:                  b64Std(acc.kekSalt),
		EncryptedKey:             encKey,
		KeyDecryptionNonce:       keyNonce,
		PublicKey:                b64Std(pk),
		EncryptedSecretKey:       encSecret,
		SecretKeyDecryptionNonce: secretNonce,
		MemLimit:                 8192,
		OpsLimit:                 3,
	}
	// seal the token to the account's public key, museum style
	esk := mustRand(32)
	epk, err := boxPublicKey(esk)
	if err != nil {
		t.Fatal(err)
	}
	nh, _ := blake2b.New(24, nil)
	nh.Write(epk)
	nh.Write(pk)
	var nonce [24]byte
	copy(nonce[:], nh.Sum(nil))
	var pkArr, eskArr [32]byte
	copy(pkArr[:], pk)
	copy(eskArr[:], esk)
	sealed := append(append([]byte{}, epk...), box.Seal(nil, acc.tokenRaw, &nonce, &pkArr, &eskArr)...)
	acc.encTokenB64 = b64Std(sealed)
	return acc
}

// sealB64 secretbox-seals plain under key and returns cipher/nonce base64.
func sealB64(plain, key []byte) (cipherB64, nonceB64 string) {
	nonce := make([]byte, 24)
	rand.Read(nonce)
	var k [32]byte
	var n [24]byte
	copy(n[:], nonce)
	copy(k[:], key)
	return b64Std(secretbox.Seal(nil, plain, &n, &k)), b64Std(nonce)
}

// ---------------------------------------------------------------------------
// mock museum auth server
// ---------------------------------------------------------------------------

type srpServerSession struct {
	m1 []byte
	m2 []byte
}

type mockAuthServer struct {
	*httptest.Server

	acc       *srpAccount
	twoFA     bool // verify-session returns a two-factor session instead
	passkey   bool // verify-session returns a passkey session
	srpLogins int
	mu        sync.Mutex
	sessions  map[string]*srpServerSession
	paths     []string
}

func newMockAuthServer(t *testing.T, acc *srpAccount) *mockAuthServer {
	t.Helper()
	m := &mockAuthServer{acc: acc, sessions: map[string]*srpServerSession{}}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.Server.Close)
	return m
}

func (m *mockAuthServer) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.paths = append(m.paths, r.URL.Path)
	m.mu.Unlock()

	fail := func(status int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	ok := func(v any) { writeJSON(w, v) }

	switch r.URL.Path {
	case "/users/srp/attributes":
		if r.URL.Query().Get("email") != m.acc.email {
			fail(http.StatusNotFound, "account not found")
			return
		}
		ok(map[string]any{"attributes": srpAttributes{
			SrpUserID: m.acc.srpUserID,
			SrpSalt:   b64Std(m.acc.srpSalt),
			KEKSalt:   b64Std(m.acc.kekSalt),
			MemLimit:  8192,
			OpsLimit:  3,
		}})
	case "/users/srp/create-session":
		var in struct{ SrpUserID, SrpA string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		srpA, err := base64.StdEncoding.DecodeString(in.SrpA)
		if err != nil || len(srpA) != srpKeySize {
			fail(http.StatusBadRequest, "invalid srpA")
			return
		}
		b := make([]byte, 32)
		rand.Read(b)
		gb := new(big.Int).Exp(big.NewInt(srpG), new(big.Int).SetBytes(b), srpN)
		B := new(big.Int).Mod(new(big.Int).Add(new(big.Int).Mul(srpK, m.acc.v), gb), srpN)
		A := new(big.Int).SetBytes(srpA)
		u := new(big.Int).SetBytes(srpHash(padSRP(A), padSRP(B)))
		S := padSRP(new(big.Int).Exp(new(big.Int).Mul(A, new(big.Int).Exp(m.acc.v, u, srpN)), new(big.Int).SetBytes(b), srpN))
		k := srpHash(S)
		m1 := srpHash(padSRP(A), padSRP(B), S)
		sessionID := "sess-" + b64Std(k[:8])
		m.mu.Lock()
		m.sessions[sessionID] = &srpServerSession{m1: m1, m2: srpHash(padSRP(A), m1, k)}
		m.mu.Unlock()
		ok(map[string]string{"sessionID": sessionID, "srpB": b64Std(padSRP(B))})
	case "/users/srp/verify-session":
		var in struct{ SrpUserID, SessionID, SrpM1 string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		m.mu.Lock()
		sess, found := m.sessions[in.SessionID]
		m.mu.Unlock()
		m1, _ := base64.StdEncoding.DecodeString(in.SrpM1)
		if !found || string(m1) != string(sess.m1) {
			fail(http.StatusUnauthorized, "invalid srpM1")
			return
		}
		resp := map[string]any{"srpM2": b64Std(sess.m2)}
		if m.passkey {
			resp["passkeySessionID"] = "pk-1"
		}
		if m.twoFA {
			resp["twoFactorSessionIDV2"] = "tf-1"
		}
		if !m.passkey && !m.twoFA {
			resp["keyAttributes"] = m.acc.keyAttrs
			resp["encryptedToken"] = m.acc.encTokenB64
			m.mu.Lock()
			m.srpLogins++
			m.mu.Unlock()
		}
		ok(resp)
	case "/users/two-factor/verify":
		var in struct{ SessionID, Code string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if !totp.Validate(in.Code, test2FASecret) {
			fail(http.StatusUnauthorized, "invalid totp")
			return
		}
		ok(map[string]any{"keyAttributes": m.acc.keyAttrs, "encryptedToken": m.acc.encTokenB64})
	case "/users/session-validity/v2":
		if r.Header.Get(authTokenHeader) != m.acc.tokenB64 {
			fail(http.StatusUnauthorized, "invalid token")
			return
		}
		ok(map[string]any{"hasSetKeys": true, "keyAttributes": m.acc.keyAttrs})
	default:
		fail(http.StatusNotFound, "unexpected path "+r.URL.Path)
	}
}

func (m *mockAuthServer) seenPaths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.paths))
	copy(out, m.paths)
	return out
}

func hasPath(paths []string, prefix string) bool {
	for _, p := range paths {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// in-memory db for the token write-back
// ---------------------------------------------------------------------------

var (
	setupDBOnce sync.Once
	setupDBErr  error
	authRowSeq  int
)

// setupTestDB backs internal/db with an in-memory SQLite so the
// op.MustSaveDriverStorage write-back during password login really persists.
func setupTestDB(t *testing.T) uint {
	t.Helper()
	setupDBOnce.Do(func() {
		var gormDB *gorm.DB
		gormDB, setupDBErr = gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		if setupDBErr == nil {
			db.Init(gormDB)
		}
	})
	if setupDBErr != nil {
		t.Fatalf("failed to set up test database: %v", setupDBErr)
	}
	authRowSeq++
	st := &model.Storage{Driver: "Ente", MountPath: fmt.Sprintf("/ente-auth-test-%d", authRowSeq)}
	if err := db.CreateStorage(st); err != nil {
		t.Fatalf("failed to create storage row: %v", err)
	}
	return st.ID
}

func passwordAddition(email, password, token string) Addition {
	return Addition{
		RootPath: driver.RootPath{RootFolderPath: "root"},
		Email:    email,
		Password: password,
		Token:    token,
	}
}

func newAuthDriver(t *testing.T, m *mockAuthServer, addition Addition) *Ente {
	t.Helper()
	id := setupTestDB(t)
	d := &Ente{Addition: addition}
	d.Endpoint = m.URL
	d.SetStorage(model.Storage{ID: id, Driver: "Ente", MountPath: fmt.Sprintf("/ente-auth-test-%d", authRowSeq)})
	return d
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

func TestInitPasswordLogin(t *testing.T) {
	m := newMockAuthServer(t, newSrpAccount(t, testEmail, testPassword))
	addition := passwordAddition(testEmail, testPassword, "")
	addition.MasterKey = b64Std(m.acc.masterKey)
	addition.SecretKey = b64Std(m.acc.secretKey)
	d := newAuthDriver(t, m, addition)
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if d.Token != m.acc.tokenB64 {
		t.Fatalf("token = %q, want the freshly issued token", d.Token)
	}
	if !bytesEqual(d.masterKey, m.acc.masterKey) || !bytesEqual(d.secretKey, m.acc.secretKey) {
		t.Fatal("decrypted keys mismatch")
	}
	if d.MasterKey != "" || d.SecretKey != "" {
		t.Fatal("password login keys must be cleared from the persisted addition")
	}
	storage, err := db.GetStorageById(d.ID)
	if err != nil {
		t.Fatalf("load persisted storage: %v", err)
	}
	var persisted Addition
	if err := json.Unmarshal([]byte(storage.Addition), &persisted); err != nil {
		t.Fatalf("decode persisted addition: %v", err)
	}
	if persisted.Token != m.acc.tokenB64 || persisted.MasterKey != "" || persisted.SecretKey != "" {
		t.Fatalf("persisted addition = token %q, master_key %q, secret_key %q", persisted.Token, persisted.MasterKey, persisted.SecretKey)
	}
	if m.srpLogins != 1 {
		t.Fatalf("srpLogins = %d, want 1", m.srpLogins)
	}
	if hasPath(m.seenPaths(), "/users/session-validity") {
		t.Fatal("empty token must skip the fast path")
	}
}

func TestInitTokenFastPath(t *testing.T) {
	acc := newSrpAccount(t, testEmail, testPassword)
	m := newMockAuthServer(t, acc)
	addition := passwordAddition(testEmail, testPassword, acc.tokenB64)
	addition.MasterKey = "stale-master-key"
	addition.SecretKey = "stale-secret-key"
	d := newAuthDriver(t, m, addition)
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if hasPath(m.seenPaths(), "/users/srp/") {
		t.Fatalf("valid token must skip SRP, saw %v", m.seenPaths())
	}
	if !bytesEqual(d.masterKey, acc.masterKey) {
		t.Fatal("fast path decrypted wrong master key")
	}
	storage, err := db.GetStorageById(d.ID)
	if err != nil {
		t.Fatalf("load persisted storage: %v", err)
	}
	var persisted Addition
	if err := json.Unmarshal([]byte(storage.Addition), &persisted); err != nil {
		t.Fatalf("decode persisted addition: %v", err)
	}
	if persisted.MasterKey != "" || persisted.SecretKey != "" {
		t.Fatalf("fast-path persisted keys: master_key %q, secret_key %q", persisted.MasterKey, persisted.SecretKey)
	}
}

func TestInitTokenFallbackToSrp(t *testing.T) {
	m := newMockAuthServer(t, newSrpAccount(t, testEmail, testPassword))
	d := newAuthDriver(t, m, passwordAddition(testEmail, testPassword, "stale-token"))
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !hasPath(m.seenPaths(), "/users/srp/verify-session") {
		t.Fatalf("stale token must fall back to SRP, saw %v", m.seenPaths())
	}
	if d.Token != m.acc.tokenB64 {
		t.Fatal("token not written back after SRP login")
	}
}

func TestInitWrongPassword(t *testing.T) {
	m := newMockAuthServer(t, newSrpAccount(t, testEmail, testPassword))
	d := newAuthDriver(t, m, passwordAddition(testEmail, "wrong password", ""))
	err := d.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "密码") {
		t.Fatalf("err = %v, want wrong-password message", err)
	}
}

func TestInitTwoFANoSecret(t *testing.T) {
	m := newMockAuthServer(t, newSrpAccount(t, testEmail, testPassword))
	m.twoFA = true
	d := newAuthDriver(t, m, passwordAddition(testEmail, testPassword, ""))
	err := d.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "two_fa_secret") {
		t.Fatalf("err = %v, want two_fa_secret hint", err)
	}
}

func TestInitTwoFAWithSecret(t *testing.T) {
	m := newMockAuthServer(t, newSrpAccount(t, testEmail, testPassword))
	m.twoFA = true
	add := passwordAddition(testEmail, testPassword, "")
	add.TwoFASecret = test2FASecret
	d := newAuthDriver(t, m, add)
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if d.Token != m.acc.tokenB64 {
		t.Fatal("token missing after 2FA login")
	}
	if !hasPath(m.seenPaths(), "/users/two-factor/verify") {
		t.Fatalf("two-factor verify not called, saw %v", m.seenPaths())
	}
}

func TestInitTwoFAPrioritizedOverPasskey(t *testing.T) {
	m := newMockAuthServer(t, newSrpAccount(t, testEmail, testPassword))
	m.passkey = true
	m.twoFA = true
	add := passwordAddition(testEmail, testPassword, "")
	add.TwoFASecret = test2FASecret
	d := newAuthDriver(t, m, add)
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if d.Token != m.acc.tokenB64 {
		t.Fatal("token missing after 2FA login")
	}
	if !hasPath(m.seenPaths(), "/users/two-factor/verify") {
		t.Fatalf("two-factor verify not called, saw %v", m.seenPaths())
	}
}

func TestInitWrongTwoFASecret(t *testing.T) {
	m := newMockAuthServer(t, newSrpAccount(t, testEmail, testPassword))
	m.twoFA = true
	// a wrong secret still yields a well-formed code the mock rejects
	add := passwordAddition(testEmail, testPassword, "")
	add.TwoFASecret = "JBSWY3DPEHPK3PXQ"
	d := newAuthDriver(t, m, add)
	err := d.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "密码") {
		t.Fatalf("err = %v, want mapped 401 message", err)
	}
}

func TestInitEmailOTPAccount(t *testing.T) {
	m := newMockAuthServer(t, newSrpAccount(t, testEmail, testPassword))
	// unknown email → 404 attributes → email-OTP-only account
	d := newAuthDriver(t, m, passwordAddition("other@example.com", testPassword, ""))
	err := d.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "不支持密码登录") {
		t.Fatalf("err = %v, want unsupported message", err)
	}
}

func TestInitPasskeyUnsupported(t *testing.T) {
	m := newMockAuthServer(t, newSrpAccount(t, testEmail, testPassword))
	m.passkey = true
	d := newAuthDriver(t, m, passwordAddition(testEmail, testPassword, ""))
	err := d.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "passkey") {
		t.Fatalf("err = %v, want passkey message", err)
	}
}

func TestInitNoCredentials(t *testing.T) {
	d := &Ente{}
	err := d.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no way to authenticate") {
		t.Fatalf("err = %v, want no-credentials error", err)
	}
}

func TestMapLoginError(t *testing.T) {
	if err := mapLoginError("/p", http.StatusTooManyRequests, ""); err == nil || !strings.Contains(err.Error(), "频繁") {
		t.Fatalf("429 mapping = %v", err)
	}
	if err := mapLoginError("/p", http.StatusNotFound, "boom"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("404 mapping = %v", err)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
