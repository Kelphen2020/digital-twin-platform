// Package auth implements the identity layer: seeded/admin-maintained accounts,
// PBKDF2 password verification, stateless HS256 JWTs and role/area checks.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"digital-twin-platform/api/internal/model"
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

// User is the authenticated principal (password hash never serialized).
type User = model.UserAccount

// Errors returned by the service.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid token")
	ErrUsernameTaken      = errors.New("username already exists")
	ErrUserNotFound       = errors.New("user not found")
	ErrBadInput           = errors.New("invalid user input")
)

// Repo persists accounts. The memory implementation seeds the demo accounts;
// the store-backed implementation is used by the running Host.
type Repo interface {
	GetUserAccountByUsername(ctx context.Context, username string) (model.UserAccount, bool, error)
	GetUserAccountByID(ctx context.Context, id string) (model.UserAccount, bool, error)
	ListUserAccounts(ctx context.Context) ([]model.UserAccount, error)
	CreateUserAccount(ctx context.Context, account model.UserAccount) error
	UpdateUserAccount(ctx context.Context, account model.UserAccount) error
	DeleteUserAccount(ctx context.Context, id string) error
}

// CreateUserInput is the admin create payload.
type CreateUserInput struct {
	Username    string
	Password    string
	DisplayName string
	Role        string
	Areas       []string
	Active      bool
}

// UpdateUserInput is the admin patch payload.
type UpdateUserInput struct {
	DisplayName *string
	Password    *string
	Role        *string
	Areas       *[]string
	Active      *bool
}

// Service holds the repository, signing secret and auth mode.
type Service struct {
	mode   bool
	secret []byte
	ttl    time.Duration
	repo   Repo
}

// New builds an in-memory auth service seeded with the demo accounts.
func New(mode, secret string) *Service {
	service := &Service{
		mode:   strings.EqualFold(mode, "on"),
		secret: []byte(secret),
		ttl:    12 * time.Hour,
	}
	if service.mode && len(secret) == 0 {
		service.secret = []byte("dev-secret-change-me")
	}
	service.repo = newMemoryRepo()
	_ = service.EnsureSeed(context.Background())
	return service
}

// NewWithRepo builds an auth service over a persistent repository.
func NewWithRepo(mode, secret string, repo Repo) *Service {
	service := &Service{
		mode:   strings.EqualFold(mode, "on"),
		secret: []byte(secret),
		ttl:    12 * time.Hour,
		repo:   repo,
	}
	if service.mode && len(secret) == 0 {
		service.secret = []byte("dev-secret-change-me")
	}
	return service
}

// Enabled reports whether authentication is enforced.
func (s *Service) Enabled() bool { return s.mode }

// DevUser is the implicit administrator used when authentication is off.
func DevUser() User {
	return model.UserAccount{ID: "usr_dev", Username: "dev", DisplayName: "本地开发", Role: RoleAdmin, Active: true}
}

func seedAccounts() []struct {
	user     model.UserAccount
	password string
} {
	return []struct {
		user     model.UserAccount
		password string
	}{
		{model.UserAccount{ID: "usr_admin", Username: "admin", DisplayName: "管理员", Role: RoleAdmin, Active: true}, "Admin@123456"},
		{model.UserAccount{ID: "usr_operator", Username: "operator", DisplayName: "值班员", Role: RoleOperator, Active: true, Areas: []string{"area_gate", "area_parking"}}, "Operator@123"},
		{model.UserAccount{ID: "usr_viewer", Username: "viewer", DisplayName: "观察员", Role: RoleViewer, Active: true}, "Viewer@123"},
	}
}

// EnsureSeed writes the demo accounts when the repository is empty.
func (s *Service) EnsureSeed(ctx context.Context) error {
	accounts, err := s.repo.ListUserAccounts(ctx)
	if err != nil {
		return err
	}
	if len(accounts) > 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, seed := range seedAccounts() {
		hash, err := hashPassword(seed.password)
		if err != nil {
			return err
		}
		account := seed.user
		account.PasswordHash = string(hash)
		account.CreatedAt = now
		account.UpdatedAt = now
		if err := s.repo.CreateUserAccount(ctx, account); err != nil {
			return err
		}
	}
	return nil
}

// Login verifies credentials and returns a signed token.
func (s *Service) Login(ctx context.Context, username, password string) (string, time.Time, User, error) {
	if !s.mode {
		user := DevUser()
		return "dev", time.Now().Add(s.ttl).UTC(), user, nil
	}
	account, ok, err := s.repo.GetUserAccountByUsername(ctx, username)
	if err != nil {
		return "", time.Time{}, User{}, err
	}
	if !ok || !account.Active || !verifyPassword([]byte(account.PasswordHash), password) {
		return "", time.Time{}, User{}, ErrInvalidCredentials
	}
	account.PasswordHash = ""
	expires := time.Now().Add(s.ttl).UTC()
	token, err := s.sign(account, expires)
	if err != nil {
		return "", time.Time{}, User{}, err
	}
	return token, expires, account, nil
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
		Sub    string   `json:"sub"`
		Usr    string   `json:"usr"`
		Name   string   `json:"name"`
		Role   string   `json:"role"`
		Areas  []string `json:"areas"`
		Active bool     `json:"active"`
		Exp    int64    `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return User{}, ErrInvalidToken
	}
	if time.Now().Unix() >= claims.Exp || !claims.Active {
		return User{}, ErrInvalidToken
	}
	return model.UserAccount{
		ID: claims.Sub, Username: claims.Usr, DisplayName: claims.Name,
		Role: claims.Role, Areas: claims.Areas, Active: true,
	}, nil
}

func (s *Service) sign(user User, expires time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"sub": user.ID, "usr": user.Username, "name": user.DisplayName,
		"role": user.Role, "areas": user.Areas, "active": user.Active,
		"iat": time.Now().Unix(), "exp": expires.Unix(),
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

// Users lists accounts without password hashes.
func (s *Service) Users(ctx context.Context) ([]User, error) {
	accounts, err := s.repo.ListUserAccounts(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Username < accounts[j].Username })
	for i := range accounts {
		accounts[i].PasswordHash = ""
	}
	return accounts, nil
}

// UserExists reports whether a username is known.
func (s *Service) UserExists(ctx context.Context, username string) (bool, error) {
	_, ok, err := s.repo.GetUserAccountByUsername(ctx, username)
	return ok, err
}

// GetUser returns one account.
func (s *Service) GetUser(ctx context.Context, id string) (User, bool, error) {
	account, ok, err := s.repo.GetUserAccountByID(ctx, id)
	if err != nil || !ok {
		return User{}, ok, err
	}
	account.PasswordHash = ""
	return account, true, nil
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

// CreateUser validates and stores a new account.
func (s *Service) CreateUser(ctx context.Context, input CreateUserInput) (User, error) {
	input.Username = strings.TrimSpace(input.Username)
	if !usernamePattern.MatchString(input.Username) {
		return User{}, fmt.Errorf("%w: username must be 3-32 characters (letters, digits, _ . -)", ErrBadInput)
	}
	if err := validatePassword(input.Password); err != nil {
		return User{}, err
	}
	if !ValidRole(input.Role) {
		return User{}, fmt.Errorf("%w: unknown role", ErrBadInput)
	}
	if _, exists, err := s.repo.GetUserAccountByUsername(ctx, input.Username); err != nil {
		return User{}, err
	} else if exists {
		return User{}, ErrUsernameTaken
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		return User{}, err
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = input.Username
	}
	now := time.Now().UTC()
	account := model.UserAccount{
		ID: newID("usr"), Username: input.Username, DisplayName: displayName,
		Role: input.Role, Areas: input.Areas, Active: input.Active,
		PasswordHash: string(hash), CreatedAt: now, UpdatedAt: now,
	}
	if account.Areas == nil {
		account.Areas = []string{}
	}
	if err := s.repo.CreateUserAccount(ctx, account); err != nil {
		if errors.Is(err, model.ErrConflict) {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}
	account.PasswordHash = ""
	return account, nil
}

// UpdateUser applies an admin patch.
func (s *Service) UpdateUser(ctx context.Context, id string, input UpdateUserInput) (User, error) {
	account, ok, err := s.repo.GetUserAccountByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	if !ok {
		return User{}, ErrUserNotFound
	}
	if input.DisplayName != nil {
		account.DisplayName = strings.TrimSpace(*input.DisplayName)
		if account.DisplayName == "" {
			account.DisplayName = account.Username
		}
	}
	if input.Role != nil {
		if !ValidRole(*input.Role) {
			return User{}, fmt.Errorf("%w: unknown role", ErrBadInput)
		}
		account.Role = *input.Role
	}
	if input.Areas != nil {
		account.Areas = *input.Areas
		if account.Areas == nil {
			account.Areas = []string{}
		}
	}
	if input.Active != nil {
		account.Active = *input.Active
	}
	if input.Password != nil {
		if err := validatePassword(*input.Password); err != nil {
			return User{}, err
		}
		hash, err := hashPassword(*input.Password)
		if err != nil {
			return User{}, err
		}
		account.PasswordHash = string(hash)
	}
	account.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateUserAccount(ctx, account); err != nil {
		return User{}, err
	}
	account.PasswordHash = ""
	return account, nil
}

// DeleteUser removes an account.
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	return s.repo.DeleteUserAccount(ctx, id)
}

// validatePassword enforces the P10.0 password policy.
func validatePassword(password string) error {
	if len(password) < 10 {
		return fmt.Errorf("%w: password must be at least 10 characters", ErrBadInput)
	}
	hasLetter, hasDigit := false, false
	for _, char := range password {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z':
			hasLetter = true
		case char >= '0' && char <= '9':
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("%w: password must contain at least one letter and one digit", ErrBadInput)
	}
	return nil
}

// ValidRole reports whether a role string is known.
func ValidRole(role string) bool {
	return role == RoleAdmin || role == RoleOperator || role == RoleViewer
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

// ---- password hashing ---------------------------------------------------

const passwordIterations = 120000

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

func newID(prefix string) string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(raw)
}

// ---- memory repository (tests / auth-off demos) -------------------------

type memoryRepo struct {
	mu     sync.Mutex
	byID   map[string]model.UserAccount
	byName map[string]string
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{byID: make(map[string]model.UserAccount), byName: make(map[string]string)}
}

func (m *memoryRepo) GetUserAccountByUsername(_ context.Context, username string) (model.UserAccount, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byName[username]
	if !ok {
		return model.UserAccount{}, false, nil
	}
	return m.byID[id], true, nil
}

func (m *memoryRepo) GetUserAccountByID(_ context.Context, id string) (model.UserAccount, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	account, ok := m.byID[id]
	return account, ok, nil
}

func (m *memoryRepo) ListUserAccounts(context.Context) ([]model.UserAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.UserAccount, 0, len(m.byID))
	for _, account := range m.byID {
		out = append(out, account)
	}
	return out, nil
}

func (m *memoryRepo) CreateUserAccount(_ context.Context, account model.UserAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.byName[account.Username]; exists {
		return model.ErrConflict
	}
	m.byID[account.ID] = account
	m.byName[account.Username] = account.ID
	return nil
}

func (m *memoryRepo) UpdateUserAccount(_ context.Context, account model.UserAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.byID[account.ID]; !exists {
		return model.ErrNotFound
	}
	m.byID[account.ID] = account
	m.byName[account.Username] = account.ID
	return nil
}

func (m *memoryRepo) DeleteUserAccount(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	account, exists := m.byID[id]
	if !exists {
		return model.ErrNotFound
	}
	delete(m.byID, id)
	delete(m.byName, account.Username)
	return nil
}
