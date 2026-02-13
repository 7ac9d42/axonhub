package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
)

func NewOAuthTokenProvider(params oauth.TokenProviderParams) *oauth.TokenProvider {
	params.OAuthUrls = DefaultTokenURLs
	if params.UserAgent == "" {
		params.UserAgent = UserAgent
	}

	if params.Credentials != nil {
		params.Credentials = NormalizeOAuthCredentials(params.Credentials)
	}

	return oauth.NewTokenProvider(params)
}

func NormalizeOAuthCredentials(creds *oauth.OAuthCredentials) *oauth.OAuthCredentials {
	if creds == nil {
		return nil
	}

	if !creds.ExpiresAt.IsZero() || creds.RefreshToken != "" {
		return creds
	}

	copyCreds := *creds
	copyCreds.ExpiresAt = time.Now().AddDate(10, 0, 0)

	return &copyCreds
}

type TokenProviderParams struct {
	OAuthProvider oauth.TokenGetter
	HTTPClient    *httpclient.HttpClient
}

type TokenProvider struct {
	oauthProvider oauth.TokenGetter
	httpClient    *httpclient.HttpClient

	mu        sync.RWMutex
	token     string
	expiresAt time.Time

	sf singleflight.Group
}

type tokenExchangeResponse struct {
	Token     string `json:"token"`
	ExpiresAt any    `json:"expires_at"`
	ExpiresIn int    `json:"expires_in"`
}

func NewTokenProvider(params TokenProviderParams) *TokenProvider {
	return &TokenProvider{
		oauthProvider: params.OAuthProvider,
		httpClient:    params.HTTPClient,
	}
}

func (p *TokenProvider) Get(ctx context.Context) (*oauth.OAuthCredentials, error) {
	p.mu.RLock()
	token := p.token
	expiresAt := p.expiresAt
	p.mu.RUnlock()

	if token != "" && !time.Now().Add(1*time.Minute).After(expiresAt) {
		return &oauth.OAuthCredentials{AccessToken: token, ExpiresAt: expiresAt}, nil
	}

	v, err, _ := p.sf.Do("refresh", func() (any, error) {
		p.mu.RLock()
		cachedToken := p.token
		cachedExpiresAt := p.expiresAt
		p.mu.RUnlock()

		if cachedToken != "" && !time.Now().Add(1*time.Minute).After(cachedExpiresAt) {
			return &oauth.OAuthCredentials{AccessToken: cachedToken, ExpiresAt: cachedExpiresAt}, nil
		}

		fresh, err := p.refresh(ctx)
		if err != nil {
			return nil, err
		}

		p.mu.Lock()
		p.token = fresh.AccessToken
		p.expiresAt = fresh.ExpiresAt
		p.mu.Unlock()

		return fresh, nil
	})
	if err != nil {
		return nil, err
	}

	creds, ok := v.(*oauth.OAuthCredentials)
	if !ok {
		return nil, fmt.Errorf("singleflight returned unexpected type %T", v)
	}

	return creds, nil
}

func (p *TokenProvider) refresh(ctx context.Context) (*oauth.OAuthCredentials, error) {
	if p.oauthProvider == nil {
		return nil, fmt.Errorf("oauth provider is nil")
	}

	if p.httpClient == nil {
		return nil, fmt.Errorf("http client is nil")
	}

	githubCreds, err := p.oauthProvider.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get github oauth token: %w", err)
	}

	if strings.TrimSpace(githubCreds.AccessToken) == "" {
		return nil, fmt.Errorf("github oauth access token is empty")
	}

	req := &httpclient.Request{
		Method: http.MethodGet,
		URL:    CopilotTokenURL,
		Headers: http.Header{
			"Authorization":         []string{"token " + githubCreds.AccessToken},
			"Accept":                []string{"application/json"},
			"User-Agent":            []string{UserAgent},
			"Editor-Version":        []string{EditorVersion},
			"Editor-Plugin-Version": []string{EditorPluginVersion},
		},
	}

	resp, err := p.httpClient.Do(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body := strings.TrimSpace(string(resp.Body))
		if len(body) > 256 {
			body = body[:256]
		}

		return nil, fmt.Errorf("copilot token exchange failed: status=%d body=%s", resp.StatusCode, body)
	}

	var parsed tokenExchangeResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return nil, fmt.Errorf("decode copilot token response: %w", err)
	}

	if strings.TrimSpace(parsed.Token) == "" {
		return nil, fmt.Errorf("copilot token response missing token")
	}

	expiresAt := parseExpiresAt(parsed, time.Now())

	return &oauth.OAuthCredentials{
		AccessToken: parsed.Token,
		ExpiresAt:   expiresAt,
		TokenType:   "bearer",
	}, nil
}

func parseExpiresAt(parsed tokenExchangeResponse, now time.Time) time.Time {
	if parsed.ExpiresIn > 0 {
		return now.Add(time.Duration(parsed.ExpiresIn) * time.Second)
	}

	switch v := parsed.ExpiresAt.(type) {
	case float64:
		if v > 0 {
			return time.Unix(int64(v), 0)
		}
	case int64:
		if v > 0 {
			return time.Unix(v, 0)
		}
	case json.Number:
		if unixSeconds, err := v.Int64(); err == nil && unixSeconds > 0 {
			return time.Unix(unixSeconds, 0)
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			if unixSeconds, err := strconv.ParseInt(trimmed, 10, 64); err == nil && unixSeconds > 0 {
				return time.Unix(unixSeconds, 0)
			}

			if ts, err := time.Parse(time.RFC3339, trimmed); err == nil {
				return ts
			}
		}
	}

	return now.Add(time.Duration(math.Max(float64(25*time.Minute), float64(1*time.Minute))))
}
