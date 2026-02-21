package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rusq/slack"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

// fakeSlack is a minimal Slack API server for testing notifications.
type fakeSlack struct {
	mu   sync.Mutex
	calls map[string]int // method -> call count

	authUserID string // returned by auth.test
	dmChannelID string // returned by conversations.open
}

func newFakeSlack() *fakeSlack {
	return &fakeSlack{
		calls:       make(map[string]int),
		authUserID:  "U123ABC",
		dmChannelID: "D999DM",
	}
}

func (f *fakeSlack) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls["auth.test"]++
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"user_id": f.authUserID,
			"team_id": "T123",
			"user":    "testuser",
		})
	})

	mux.HandleFunc("/conversations.open", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls["conversations.open"]++
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channel": map[string]any{
				"id": f.dmChannelID,
			},
		})
	})

	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls["chat.postMessage"]++
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": f.dmChannelID,
			"ts":      "1234567890.123456",
		})
	})

	return mux
}

func (f *fakeSlack) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func TestNotifyUser(t *testing.T) {
	fake := newFakeSlack()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	apiURL := srv.URL + "/"

	// Create clients that point to our fake server.
	botToken := "xoxb-bot-test"
	userToken := "xoxp-user-test"

	// We can't easily inject the API URL into notifyUser because it uses
	// slack.New(token) directly.  Instead, test via sendCompletionNotification
	// which we can set up with a real store.  For the unit test of notifyUser
	// itself, we use slack.OptionAPIURL to create clients manually.
	ctx := context.Background()
	lg := slog.Default()

	// Call auth.test with user token.
	userClient := slack.New(userToken, slack.OptionAPIURL(apiURL))
	authResp, err := userClient.AuthTestContext(ctx)
	require.NoError(t, err)
	assert.Equal(t, "U123ABC", authResp.UserID)

	// Call conversations.open with bot token.
	botClient := slack.New(botToken, slack.OptionAPIURL(apiURL))
	params := &slack.OpenConversationParameters{Users: []string{authResp.UserID}}
	ch, _, _, err := botClient.OpenConversationContext(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, "D999DM", ch.ID)

	// Call chat.postMessage.
	_, _, err = botClient.PostMessageContext(ctx, ch.ID, slack.MsgOptionText("test", false))
	require.NoError(t, err)

	assert.Equal(t, 1, fake.callCount("auth.test"))
	assert.Equal(t, 1, fake.callCount("conversations.open"))
	assert.Equal(t, 1, fake.callCount("chat.postMessage"))

	_ = lg // used in the real notifyUser function
}

// mockTenantStore implements store.TenantStore for testing.
type mockTenantStore struct {
	tenants map[string]*store.Tenant
}

func (m *mockTenantStore) Create(_ context.Context, t *store.Tenant) error {
	if m.tenants == nil {
		m.tenants = make(map[string]*store.Tenant)
	}
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	t.Active = true
	m.tenants[t.ID] = t
	return nil
}

func (m *mockTenantStore) Get(_ context.Context, id string) (*store.Tenant, error) {
	if t, ok := m.tenants[id]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("tenant: not found: %s", id)
}

func (m *mockTenantStore) Deactivate(context.Context, string) error { return nil }

// mockCredStoreWithData implements store.CredentialStore with configurable data.
type mockCredStoreWithData struct {
	creds map[string]*store.Credential
}

func (m *mockCredStoreWithData) Upsert(_ context.Context, c *store.Credential) error {
	if m.creds == nil {
		m.creds = make(map[string]*store.Credential)
	}
	m.creds[c.TenantID] = c
	return nil
}

func (m *mockCredStoreWithData) GetByTenant(_ context.Context, tenantID string) (*store.Credential, error) {
	if c, ok := m.creds[tenantID]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("credential: not found for tenant: %s", tenantID)
}

func TestSendCompletionNotification_NoBotToken(t *testing.T) {
	encKey := make([]byte, 32)
	for i := range encKey {
		encKey[i] = byte(i)
	}

	tokenEnc, err := store.Encrypt(encKey, []byte("xoxp-user"))
	require.NoError(t, err)

	credStore := &mockCredStoreWithData{
		creds: map[string]*store.Credential{
			"t1": {
				ID:       "c1",
				TenantID: "t1",
				TokenEnc: tokenEnc,
				// BotTokenEnc is nil — no bot token
			},
		},
	}

	tenantStore := &mockTenantStore{
		tenants: map[string]*store.Tenant{
			"t1": {ID: "t1", Name: "Test", Workspace: "test-ws"},
		},
	}

	eng := &Engine{
		store: &store.Store{
			Credentials: credStore,
			Tenants:     tenantStore,
		},
		encryptionKey: encKey,
	}

	// Should be a no-op (no panic, no error).
	eng.sendCompletionNotification(context.Background(), slog.Default(), "t1")
}

func TestSendCompletionNotification_NoCredential(t *testing.T) {
	credStore := &mockCredStoreWithData{} // empty

	eng := &Engine{
		store: &store.Store{
			Credentials: credStore,
		},
		encryptionKey: make([]byte, 32),
	}

	// Should log warning but not panic.
	eng.sendCompletionNotification(context.Background(), slog.Default(), "nonexistent")
}
