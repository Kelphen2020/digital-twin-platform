// Package auth implements the P2.0 identity layer: seeded demo users, bcrypt
// password verification, stateless HS256 JWTs and role/area permission checks.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Roles.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// Permissions.
const (
	PermRead              = "read"
	PermEventAction       = "event.action"
	PermOrderWrite        = "order.write"
	PermCameraWrite       = "camera.write"
	PermCameraCalibration = "camera.calibration"
	PermCameraRules       = "camera.rules"
	PermAdmin             = "admin"
)

// User is the authenticated principal.
type User struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Role        string   `json:"role"`
	Areas       []string `json:"areas"`
}

// ErrInvalidCredentials is returned by Login on a bad username/password.
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrInvalidToken is returned by Verify on a malformed or expired token.
var ErrInvalidToken = errors.New("invalid token")

type account struct {
	user User
	hash []byte
}

// Service holds the seeded users, the signing secret and the auth mode.
type Service struct {
	mode   bool
	secret []byte
	ttl    time.Duration
	users  map[string]account
}

// New builds the auth service. mode is "on" or "off"; off grants an implicit
// admin used by local demos and tests.
func New(mode, secret string) *Service {
	service := &Service{
		mode:   strings.EqualFold(mode, "on"),
		secret: []byte(secret),
		ttl:    12 * time.Hour,
		users:  map[string]account{},
	}
	if service.mode && len(secret) == 0 {
		service.secret = []byte("dev-secret-change-me")
	}
	service.seed()
	return service
}

// Enabled reports whether authentication is enforced.
func (s *Service) Enabled() bool { return s.mode }

// DevUser is the implicit administrator used when authentication is off.
func DevUser() User {
	return User{ID: "usr_dev", Username: "dev", DisplayName: "本地开发", Role: RoleAdmin}
}

func (s *Service) seed() {
	seed := []struct {
		user     User
		password string
	}{
		{User{ID: "usr_admin", Username: "admin", DisplayName: "管理员", Role: RoleAdmin}, "Admin@123456"},
		{User{ID: "usr_operator", Username: "operator", DisplayName: "值班员", Role: RoleOperator, Areas: []string{"area_gate", "area_parking"}}, "Operator@123"},
		{User{ID: "usr_viewer", Username: "viewer", DisplayName: "观察员", Role: RoleViewer}, "Viewer@123"},
	}
	for _, item := range seed {
		hash, err := hashPassword(item.password)
		if err != nil {
			continue
		}
		s.users[item.user.Username] = account{user: item.user, hash: hash}
	}
}

// Login verifies credentials and returns a signed token.
func (s *Service) Login(username, password string) (string, time.Time, User, error) {
	if !s.mode {
		user := DevUser()
		return "dev", time.Now().Add(s.ttl).UTC(), user, nil
	}
	account, ok := s.users[username]
	if !ok || !verifyPassword(account.hash, password) {
		return "", time.Time{}, User{}, ErrInvalidCredentials
	}
	expires := time.Now().Add(s.ttl).UTC()
	token, err := s.sign(account.user, expires)
	if err != nil {
		return "", time.Time{}, User{}, err
	}
	return token, expires, account.user, nil
}

// Verify validates a token and returns its principal.
func (s *Service) Verify(token string) (User, error) {
	if !s.mode {
		return DevUser(), nil
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return User{}, ErrInvalidToken
	}
	signed := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(parts[2]), []byte(s.signature(signed))) {
		return User{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return User{}, ErrInvalidToken
	}
	var claims struct {
		Sub   string   `json:"sub"`
		Usr   string   `json:"usr"`
		Role  string   `json:"role"`
		Areas []string `json:"areas"`
		Exp   int64    `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return User{}, ErrInvalidToken
	}
	if time.Now().Unix() >= claims.Exp {
		return User{}, ErrInvalidToken
	}
	displayName := claims.Usr
	if account, ok := s.users[claims.Usr]; ok {
		displayName = account.user.DisplayName
	}
	return User{ID: claims.Sub, Username: claims.Usr, DisplayName: displayName, Role: claims.Role, Areas: claims.Areas}, nil
}

func (s *Service) sign(user User, expires time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"sub": user.ID, "usr": user.Username, "role": user.Role,
		"areas": user.Areas, "iat": time.Now().Unix(), "exp": expires.Unix(),
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signed := header + "." + payload
	return signed + "." + s.signature(signed), nil
}

func (s *Service) signature(signed string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(signed))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

const passwordIterations = 120000

// hashPassword derives a salted PBKDF2-HMAC-SHA256 record:
// "pbkdf2$<iterations>$<salt>$<hash>".
func hashPassword(password string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	derived := pbkdf2SHA256([]byte(password), salt, passwordIterations, 32)
	return []byte(fmt.Sprintf("pbkdf2$%d$%s$%s", passwordIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived))), nil
}

func verifyPassword(encoded []byte, password string) bool {
	parts := strings.Split(string(encoded), "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// pbkdf2SHA256 is the RFC 8018 PBKDF2 with HMAC-SHA256 (no external module).
func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	blocks := (keyLen + sha256.Size - 1) / sha256.Size
	out := make([]byte, 0, blocks*sha256.Size)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		var counter [4]byte
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		mac.Write(counter[:])
		u := mac.Sum(nil)
		accumulated := make([]byte, len(u))
		copy(accumulated, u)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range accumulated {
				accumulated[j] ^= u[j]
			}
		}
		out = append(out, accumulated...)
	}
	return out[:keyLen]
}

// Can reports whether a role holds a permission.
func Can(role, permission string) bool {
	switch permission {
	case PermRead:
		return role == RoleViewer || role == RoleOperator || role == RoleAdmin
	case PermEventAction, PermOrderWrite, PermCameraWrite:
		return role == RoleOperator || role == RoleAdmin
	case PermCameraCalibration, PermCameraRules, PermAdmin:
		return role == RoleAdmin
	default:
		return false
	}
}

// InScope reports whether the user may touch a camera in areaID. An empty area
// list means "all areas"; admins are never scoped.
func InScope(user User, areaID string) bool {
	if user.Role == RoleAdmin || len(user.Areas) == 0 {
		return true
	}
	if areaID == "" {
		return true
	}
	for _, area := range user.Areas {
		if area == areaID {
			return true
		}
	}
	return false
}
