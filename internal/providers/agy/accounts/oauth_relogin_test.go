package accounts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReloginWritesTargetAccount(t *testing.T) {
	m, err := NewManager(Options{DataDir: t.TempDir(), HostHome: t.TempDir()})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// 先创建一个账号，再将其 Email 清空、状态置为异常并删除其 home 与 token 文件，模拟凭证丢失。
	created, err := m.mergeAndSaveAccount(OAuthTokenFile{
		AuthMethod: "consumer",
		Token:      OAuthTokenData{AccessToken: "old-token"},
	}, "initial@example.com", "")
	if err != nil {
		t.Fatalf("mergeAndSaveAccount failed: %v", err)
	}

	m.mu.Lock()
	for i := range m.index.Accounts {
		if m.index.Accounts[i].ID == created.ID {
			m.index.Accounts[i].Email = ""
			m.index.Accounts[i].Status = StatusInvalid
			m.index.Accounts[i].StatusReason = "missing token"
		}
	}
	if err := m.saveIndexLocked(); err != nil {
		m.mu.Unlock()
		t.Fatalf("saveIndexLocked failed: %v", err)
	}
	beforeCount := len(m.index.Accounts)
	m.mu.Unlock()

	if err := os.RemoveAll(m.accountHomeDir(created.ID)); err != nil {
		t.Fatalf("RemoveAll account home failed: %v", err)
	}

	updated, err := m.reloginSaveAccount(created.ID, OAuthTokenFile{
		AuthMethod: "consumer",
		Token:      OAuthTokenData{AccessToken: "relogin-token"},
	}, "rebound@example.com", "Rebound User")
	if err != nil {
		t.Fatalf("reloginSaveAccount failed: %v", err)
	}

	if updated.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, updated.ID)
	}
	if updated.Status != StatusActive || updated.StatusReason != "" {
		t.Errorf("expected StatusActive with empty reason, got status=%q reason=%q", updated.Status, updated.StatusReason)
	}
	if updated.Email != "rebound@example.com" {
		t.Errorf("expected Email rebound@example.com, got %q", updated.Email)
	}
	if updated.Name != "Rebound User" {
		t.Errorf("expected Name Rebound User, got %q", updated.Name)
	}

	accs := m.List()
	if len(accs) != beforeCount {
		t.Fatalf("expected account count %d, got %d", beforeCount, len(accs))
	}

	tokenPath := filepath.Join(m.accountHomeDir(created.ID), ".gemini", "antigravity-cli", "antigravity-oauth-token")
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read relogin token file: %v", err)
	}
	var tf OAuthTokenFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		t.Fatalf("unmarshal relogin token file: %v", err)
	}
	if tf.Token.AccessToken != "relogin-token" {
		t.Errorf("expected relogin-token on disk, got %q", tf.Token.AccessToken)
	}
}

func TestReloginRejectsDifferentEmail(t *testing.T) {
	m, err := NewManager(Options{DataDir: t.TempDir(), HostHome: t.TempDir()})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	accA, err := m.mergeAndSaveAccount(OAuthTokenFile{
		AuthMethod: "consumer",
		Token:      OAuthTokenData{AccessToken: "token-a"},
	}, "a@example.com", "User A")
	if err != nil {
		t.Fatalf("create accA failed: %v", err)
	}
	accB, err := m.mergeAndSaveAccount(OAuthTokenFile{
		AuthMethod: "consumer",
		Token:      OAuthTokenData{AccessToken: "token-b"},
	}, "b@example.com", "User B")
	if err != nil {
		t.Fatalf("create accB failed: %v", err)
	}

	tokenPathA := filepath.Join(m.accountHomeDir(accA.ID), ".gemini", "antigravity-cli", "antigravity-oauth-token")
	tokenPathB := filepath.Join(m.accountHomeDir(accB.ID), ".gemini", "antigravity-cli", "antigravity-oauth-token")
	beforeA, err := os.ReadFile(tokenPathA)
	if err != nil {
		t.Fatalf("read token A: %v", err)
	}
	beforeB, err := os.ReadFile(tokenPathB)
	if err != nil {
		t.Fatalf("read token B: %v", err)
	}
	beforeCount := len(m.List())

	_, err = m.reloginSaveAccount(accA.ID, OAuthTokenFile{
		AuthMethod: "consumer",
		Token:      OAuthTokenData{AccessToken: "new-token-for-b"},
	}, "b@example.com", "User B")
	if !errors.Is(err, ErrReloginEmailMismatch) {
		t.Fatalf("expected ErrReloginEmailMismatch, got %v", err)
	}

	if got := len(m.List()); got != beforeCount {
		t.Fatalf("expected account count %d, got %d", beforeCount, got)
	}

	afterA, err := os.ReadFile(tokenPathA)
	if err != nil || string(afterA) != string(beforeA) {
		t.Fatalf("target account token file modified on mismatch: err=%v", err)
	}
	afterB, err := os.ReadFile(tokenPathB)
	if err != nil || string(afterB) != string(beforeB) {
		t.Fatalf("other account token file modified on mismatch: err=%v", err)
	}
}

func TestReloginMissingAccount(t *testing.T) {
	m, err := NewManager(Options{DataDir: t.TempDir(), HostHome: t.TempDir()})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	_, err = m.reloginSaveAccount("non-existent-id", OAuthTokenFile{
		AuthMethod: "consumer",
		Token:      OAuthTokenData{AccessToken: "unused"},
	}, "user@example.com", "User")
	if !errors.Is(err, ErrAccountGone) {
		t.Fatalf("expected ErrAccountGone, got %v", err)
	}
}
