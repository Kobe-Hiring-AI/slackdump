package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rusq/slack"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

// notifyUser sends a DM to the Slack user (resolved from userToken) via the
// bot token informing them that the export has completed.  All errors are
// logged at Warn level and never returned — notification is best-effort.
func notifyUser(ctx context.Context, lg *slog.Logger, botToken, userToken, workspaceName string) {
	// Resolve the user's Slack ID from the user token.
	userClient := slack.New(userToken)
	authResp, err := userClient.AuthTestContext(ctx)
	if err != nil {
		lg.WarnContext(ctx, "notify: auth.test failed", "error", err)
		return
	}
	userID := authResp.UserID
	if userID == "" {
		lg.WarnContext(ctx, "notify: auth.test returned empty user ID")
		return
	}

	// Open a DM channel via the bot token.
	botClient := slack.New(botToken)
	params := &slack.OpenConversationParameters{Users: []string{userID}}
	ch, _, _, err := botClient.OpenConversationContext(ctx, params)
	if err != nil {
		lg.WarnContext(ctx, "notify: conversations.open failed", "error", err, "user_id", userID)
		return
	}

	// Send the completion message.
	msg := fmt.Sprintf("Your Slack export for *%s* has completed.", workspaceName)
	if _, _, err := botClient.PostMessageContext(ctx, ch.ID, slack.MsgOptionText(msg, false)); err != nil {
		lg.WarnContext(ctx, "notify: chat.postMessage failed", "error", err, "channel", ch.ID)
		return
	}

	lg.InfoContext(ctx, "notify: DM sent", "user_id", userID, "workspace", workspaceName)
}

// sendCompletionNotification decrypts the bot + user tokens for the given
// tenant and sends a completion DM.  If the tenant has no bot token stored
// the call is a no-op.
func (e *Engine) sendCompletionNotification(ctx context.Context, lg *slog.Logger, tenantID string) {
	cred, err := e.store.Credentials.GetByTenant(ctx, tenantID)
	if err != nil {
		lg.WarnContext(ctx, "notify: get credentials", "error", err)
		return
	}
	if len(cred.BotTokenEnc) == 0 {
		return // no bot token — nothing to do
	}

	botToken, err := store.Decrypt(e.encryptionKey, cred.BotTokenEnc)
	if err != nil {
		lg.WarnContext(ctx, "notify: decrypt bot token", "error", err)
		return
	}
	userToken, err := store.Decrypt(e.encryptionKey, cred.TokenEnc)
	if err != nil {
		lg.WarnContext(ctx, "notify: decrypt user token", "error", err)
		return
	}

	tenant, err := e.store.Tenants.Get(ctx, tenantID)
	if err != nil {
		lg.WarnContext(ctx, "notify: get tenant", "error", err)
		return
	}

	notifyUser(ctx, lg, string(botToken), string(userToken), tenant.Workspace)
}
