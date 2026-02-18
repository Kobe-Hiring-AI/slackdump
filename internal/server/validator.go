package server

import (
	"context"
	"fmt"

	"github.com/rusq/slackdump/v4/auth"
)

// slackValidator validates Slack credentials by calling the Slack API.
type slackValidator struct{}

func (v *slackValidator) Validate(ctx context.Context, token, cookie string) (workspace, teamID string, err error) {
	provider, err := auth.NewValueAuth(token, cookie)
	if err != nil {
		return "", "", fmt.Errorf("invalid credentials: %w", err)
	}
	resp, err := provider.Test(ctx)
	if err != nil {
		return "", "", fmt.Errorf("credential test failed: %w", err)
	}
	return resp.Team, resp.TeamID, nil
}

func (v *slackValidator) ValidateCookieOnly(ctx context.Context, workspace, cookie string) (token, teamID string, err error) {
	provider, err := auth.NewCookieOnlyAuth(ctx, workspace, cookie)
	if err != nil {
		return "", "", fmt.Errorf("cookie auth failed: %w", err)
	}
	resp, err := provider.Test(ctx)
	if err != nil {
		return "", "", fmt.Errorf("credential test failed: %w", err)
	}
	return provider.SlackToken(), resp.TeamID, nil
}
