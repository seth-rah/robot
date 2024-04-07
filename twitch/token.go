package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
)

// Validate checks the status of an access token.
// If the API response indicates that the access token is invalid, the returned
// error wraps [ErrNeedRefresh].
// The returned Validation may be non-nil even if the error is also non-nil.
func Validate(ctx context.Context, client *http.Client, tok *oauth2.Token) (*Validation, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://id.twitch.tv/oauth2/validate", nil)
	if err != nil {
		return nil, fmt.Errorf("couldn't make validate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't validate access token: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("couldn't read token validation response: %w", err)
	}
	resp.Body.Close()
	var s Validation
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("couldn't unmarshal token validation response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK: // do nothing
	case http.StatusUnauthorized:
		err = fmt.Errorf("token validation failed: %s (%w)", s.Message, ErrNeedRefresh)
	default:
		err = fmt.Errorf("token validation failed: %s (%s)", s.Message, resp.Status)
	}
	return &s, err
}

// Validation describes an access token's validation status.
type Validation struct {
	ClientID  string   `json:"client_id"`
	Login     string   `json:"login"`
	Scopes    []string `json:"scopes"`
	UserID    string   `json:"user_id"`
	ExpiresIn int      `json:"expires_in"`

	Message string `json:"message"`
	Status  int    `json:"status"`
}

// ErrNeedRefresh is an error returned by Twitch API operations when a response
// indicates that the access token needs to be refreshed.
var ErrNeedRefresh = errors.New("need refresh")
