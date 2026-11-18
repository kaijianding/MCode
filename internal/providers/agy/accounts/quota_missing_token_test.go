package accounts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefreshQuotaMissingTokenFileMarksInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	defer srv.Close()

	newTestManager := func(t *testing.T) (*Manager, string) {
		t.Helper()
		dataDir := t.TempDir()
		mgr, err := NewManager(Options{
			DataDir:    dataDir,
			HostHome:   t.TempDir(),
			HTTPClient: srv.Client(),
			QuotaHosts: []string{srv.URL},
			Endpoints: OAuthEndpoints{
				TokenURL: srv.URL + "/token",
			},
		})
		if err != nil {
			t.Fatalf("NewManager failed: %v", err)
		}
		return mgr, dataDir
	}

	t.Run("missing token file marks isolated account invalid without path", func(t *testing.T) {
		mgr, dataDir := newTestManager(t)
		acc, err := mgr.mergeAndSaveAccount(OAuthTokenFile{
			AuthMethod: "consumer",
			Token: OAuthTokenData{
				AccessToken:  "valid-token",
				RefreshToken: "valid-refresh",
				TokenType:    "Bearer",
				Expiry:       time.Now().Add(time.Hour),
			},
		}, "missing@example.com", "Missing Token User")
		if err != nil {
			t.Fatalf("mergeAndSaveAccount failed: %v", err)
		}

		tokenPath := filepath.Join(mgr.accountHomeDir(acc.ID), ".gemini", "antigravity-cli", "antigravity-oauth-token")
		if err := os.Remove(tokenPath); err != nil {
			t.Fatalf("remove token file failed: %v", err)
		}

		if err := mgr.RefreshQuota(context.Background(), acc.ID); err == nil {
			t.Fatal("expected RefreshQuota error when token file is missing")
		}

		got, ok := mgr.Get(acc.ID)
		if !ok {
			t.Fatalf("account %s not found", acc.ID)
		}
		if got.Status != StatusInvalid {
			t.Fatalf("expected Status %q, got %q", StatusInvalid, got.Status)
		}
		if got.StatusReason != errTokenFileMissing.Error() {
			t.Fatalf("expected StatusReason %q, got %q", errTokenFileMissing.Error(), got.StatusReason)
		}
		if strings.Contains(got.StatusReason, tokenPath) || strings.Contains(got.StatusReason, dataDir) || strings.Contains(got.StatusReason, "antigravity-oauth-token") {
			t.Fatalf("StatusReason should not contain file path, got %q", got.StatusReason)
		}
		if !got.QuotaStale {
			t.Fatal("expected QuotaStale true")
		}
	})

	t.Run("invalid json token file keeps isolated account active", func(t *testing.T) {
		mgr, _ := newTestManager(t)
		acc, err := mgr.mergeAndSaveAccount(OAuthTokenFile{
			AuthMethod: "consumer",
			Token: OAuthTokenData{
				AccessToken:  "valid-token",
				RefreshToken: "valid-refresh",
				TokenType:    "Bearer",
				Expiry:       time.Now().Add(time.Hour),
			},
		}, "badjson@example.com", "Bad JSON User")
		if err != nil {
			t.Fatalf("mergeAndSaveAccount failed: %v", err)
		}

		tokenPath := filepath.Join(mgr.accountHomeDir(acc.ID), ".gemini", "antigravity-cli", "antigravity-oauth-token")
		if err := os.WriteFile(tokenPath, []byte("{invalid json"), 0600); err != nil {
			t.Fatalf("write invalid json failed: %v", err)
		}

		if err := mgr.RefreshQuota(context.Background(), acc.ID); err == nil {
			t.Fatal("expected RefreshQuota error when token file contains invalid JSON")
		}

		got, ok := mgr.Get(acc.ID)
		if !ok {
			t.Fatalf("account %s not found", acc.ID)
		}
		if got.Status != StatusActive {
			t.Fatalf("expected Status %q, got %q", StatusActive, got.Status)
		}
		if !got.QuotaStale {
			t.Fatal("expected QuotaStale true")
		}
	})
}
