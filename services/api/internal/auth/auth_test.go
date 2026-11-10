package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/store"
)

func seededService(t *testing.T) *auth.Service {
	t.Helper()
	service := auth.New("on", "test-secret")
	if err := service.EnsureSeed(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return service
}

func TestLoginAndVerify(t *testing.T) {
	service := seededService(t)
	ctx := context.Background()

	token, expires, user, err := service.Login(ctx, "admin", "Admin@123456")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if token == "" || user.Username != "admin" || user.Role != auth.RoleAdmin {
		t.Fatalf("unexpected login result: %+v", user)
	}
	if time.Until(expires) <= 0 {
		t.Fatalf("token expiry in the past: %v", expires)
	}
	principal, err := service.Verify(token)
	if err != nil || principal.ID != user.ID || principal.Role != auth.RoleAdmin {
		t.Fatalf("verify: %v %+v", err, principal)
	}
	if principal.PasswordHash != "" {
		t.Fatal("password hash leaked into principal")
	}

	if _, _, _, err := service.Login(ctx, "admin", "wrong-password"); err != auth.ErrInvalidCredentials {
		t.Fatalf("wrong password should be ErrInvalidCredentials, got %v", err)
	}
	if _, _, _, err := service.Login(ctx, "nobody", "whatever"); err != auth.ErrInvalidCredentials {
		t.Fatalf("unknown user should be ErrInvalidCredentials, got %v", err)
	}

	// Tampered and malformed tokens are rejected.
	if _, err := service.Verify(token + "x"); err != auth.ErrInvalidToken {
		t.Fatalf("tampered token: %v", err)
	}
	for _, bad := range []string{"", "abc", "a.b.c", strings.Repeat("x", 40)} {
		if _, err := service.Verify(bad); err != auth.ErrInvalidToken {
			t.Fatalf("token %q should be invalid, got %v", bad, err)
		}
	}
}

func TestTokenTTLAndRevocation(t *testing.T) {
	service := seededService(t)
	ctx := context.Background()
	service.SetAccessTTL(time.Millisecond)
	token, _, _, err := service.Login(ctx, "viewer", "Viewer@123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := service.Verify(token); err != auth.ErrInvalidToken {
		t.Fatalf("expired token should be invalid, got %v", err)
	}
	service.SetAccessTTL(time.Hour)

	token, _, user, err := service.Login(ctx, "viewer", "Viewer@123")
	if err != nil {
		t.Fatalf("relogin: %v", err)
	}
	if _, err := service.Verify(token); err != nil {
		t.Fatalf("fresh token should verify: %v", err)
	}
	if _, err := service.RevokeUserSessions(ctx, user.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Revocation bumps the version baked into the token claims.
	revoked, err := service.Verify(token)
	if err != nil {
		t.Fatalf("verify after revoke: %v", err)
	}
	account, ok, err := service.GetUser(ctx, user.ID)
	if err != nil || !ok {
		t.Fatalf("load account: %v %v", ok, err)
	}
	if revoked.TokenVersion == account.TokenVersion {
		t.Fatalf("token version was not bumped: %d", account.TokenVersion)
	}
}

func TestUsersCRUDAndPasswordPolicy(t *testing.T) {
	service := seededService(t)
	ctx := context.Background()

	users, err := service.Users(ctx)
	if err != nil || len(users) != 3 {
		t.Fatalf("seeded users: %v (%d)", err, len(users))
	}
	for _, user := range users {
		if user.PasswordHash != "" {
			t.Fatalf("hash leaked for %s", user.Username)
		}
	}

	created, err := service.CreateUser(ctx, auth.CreateUserInput{
		Username: "qa_user", Password: "QaPass1234", DisplayName: "QA", Role: auth.RoleViewer, Active: true,
	})
	if err != nil || created.ID == "" {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.CreateUser(ctx, auth.CreateUserInput{
		Username: "qa_user", Password: "QaPass1234", Role: auth.RoleViewer,
	}); err != auth.ErrUsernameTaken {
		t.Fatalf("duplicate username: %v", err)
	}
	invalidInputs := []auth.CreateUserInput{
		{Username: "", Password: "QaPass1234", Role: auth.RoleViewer},
		{Username: "qa_short", Password: "short1", Role: auth.RoleViewer},
		{Username: "qa_nodigit", Password: "OnlyLettersHere", Role: auth.RoleViewer},
		{Username: "qa_badrole", Password: "QaPass1234", Role: "superuser"},
		{Username: "qa_baduser", Password: "QaPass1234", Role: ""},
	}
	for index, input := range invalidInputs {
		if _, err := service.CreateUser(ctx, input); err == nil {
			t.Fatalf("invalid input %d should fail", index)
		}
	}

	// Update display name, role and password.
	display := "QA Renamed"
	role := auth.RoleOperator
	password := "QaPass5678"
	updated, err := service.UpdateUser(ctx, created.ID, auth.UpdateUserInput{
		DisplayName: &display, Role: &role, Password: &password,
	})
	if err != nil || updated.DisplayName != "QA Renamed" || updated.Role != auth.RoleOperator {
		t.Fatalf("update: %v %+v", err, updated)
	}
	if _, _, _, err := service.Login(ctx, "qa_user", "QaPass1234"); err != auth.ErrInvalidCredentials {
		t.Fatalf("old password should fail: %v", err)
	}
	if _, _, _, err := service.Login(ctx, "qa_user", "QaPass5678"); err != nil {
		t.Fatalf("new password should work: %v", err)
	}

	inactive := false
	if _, err := service.UpdateUser(ctx, created.ID, auth.UpdateUserInput{Active: &inactive}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, _, _, err := service.Login(ctx, "qa_user", "QaPass5678"); err != auth.ErrInvalidCredentials {
		t.Fatalf("inactive user should not log in: %v", err)
	}
	if _, _, err := service.GetUser(ctx, "usr_missing"); err != nil {
		t.Fatalf("get missing user should be ok=false, got %v", err)
	}
	if _, err := service.UpdateUser(ctx, "usr_missing", auth.UpdateUserInput{DisplayName: &display}); err != auth.ErrUserNotFound {
		t.Fatalf("update missing user: %v", err)
	}
	if err := service.DeleteUser(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if exists, err := service.UserExists(ctx, "qa_user"); err != nil || exists {
		t.Fatalf("user should be gone: %v %v", exists, err)
	}
}

func TestPermissionsAndDisabledMode(t *testing.T) {
	off := auth.New("off", "")
	if off.Enabled() {
		t.Fatal("mode off should report disabled")
	}
	ctx := context.Background()
	if err := off.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed off: %v", err)
	}
	if off.Enabled() {
		t.Fatal("auth-disabled service should allow everything")
	}

	_ = seededService(t)
	roles := map[string]auth.User{
		auth.RoleAdmin:    {Role: auth.RoleAdmin},
		auth.RoleOperator: {Role: auth.RoleOperator},
		auth.RoleViewer:   {Role: auth.RoleViewer},
	}
	cases := []struct {
		perm string
		want map[string]bool
	}{
		{auth.PermAdmin, map[string]bool{auth.RoleAdmin: true}},
		{auth.PermEventAction, map[string]bool{auth.RoleAdmin: true, auth.RoleOperator: true}},
		{auth.PermPatrolWrite, map[string]bool{auth.RoleAdmin: true, auth.RoleOperator: true}},
		{auth.PermAdmin, map[string]bool{auth.RoleViewer: false}},
		{"unknown.perm", map[string]bool{auth.RoleAdmin: false}},
	}
	for _, testCase := range cases {
		for role, want := range testCase.want {
			if got := auth.Can(roles[role].Role, testCase.perm); got != want {
				t.Fatalf("perm %s for %s = %v, want %v", testCase.perm, role, got, want)
			}
		}
	}
}

// TestRepoBackedService exercises the store-backed repo wiring.
func TestRepoBackedService(t *testing.T) {
	memory := store.NewMemory(100)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	service := auth.NewWithRepo("on", "secret", memory)
	ctx := context.Background()
	if err := service.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	if _, _, _, err := service.Login(ctx, "operator", "Operator@123"); err != nil {
		t.Fatalf("login via repo: %v", err)
	}
	if err := service.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed is not idempotent: %v", err)
	}
	users, err := service.Users(ctx)
	if err != nil || len(users) != 3 {
		t.Fatalf("users via repo: %v (%d)", err, len(users))
	}
}
