package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
	"github.com/looplj/axonhub/llm/transformer/openai/copilot"
)

type CopilotHandlersParams struct {
	fx.In

	CacheConfig xcache.Config
	HttpClient  *httpclient.HttpClient
}

type CopilotHandlers struct {
	stateCache xcache.Cache[copilotOAuthState]
	httpClient *httpclient.HttpClient
}

func NewCopilotHandlers(params CopilotHandlersParams) *CopilotHandlers {
	return &CopilotHandlers{
		stateCache: xcache.NewFromConfig[copilotOAuthState](params.CacheConfig),
		httpClient: params.HttpClient,
	}
}

type StartCopilotOAuthRequest struct{}

type StartCopilotOAuthResponse struct {
	SessionID string `json:"session_id"`
	AuthURL   string `json:"auth_url"`
}

type copilotOAuthState struct {
	CodeVerifier string `json:"code_verifier"`
	CreatedAt    int64  `json:"created_at"`
}

func generateCopilotCodeVerifier() (string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

func generateCopilotCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(hash[:])
}

func generateCopilotState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

func copilotOAuthCacheKey(sessionID string) string {
	return fmt.Sprintf("copilot:oauth:%s", sessionID)
}

func (h *CopilotHandlers) StartOAuth(c *gin.Context) {
	ctx := c.Request.Context()

	var req StartCopilotOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("invalid request format"))
		return
	}

	state, err := generateCopilotState()
	if err != nil {
		JSONError(c, http.StatusInternalServerError, fmt.Errorf("failed to generate oauth state: %w", err))
		return
	}

	codeVerifier, err := generateCopilotCodeVerifier()
	if err != nil {
		JSONError(c, http.StatusInternalServerError, fmt.Errorf("failed to generate code verifier: %w", err))
		return
	}

	codeChallenge := generateCopilotCodeChallenge(codeVerifier)

	cacheKey := copilotOAuthCacheKey(state)
	if err := h.stateCache.Set(ctx, cacheKey, copilotOAuthState{CodeVerifier: codeVerifier, CreatedAt: time.Now().Unix()}, xcache.WithExpiration(10*time.Minute)); err != nil {
		JSONError(c, http.StatusInternalServerError, fmt.Errorf("failed to save oauth state: %w", err))
		return
	}

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", copilot.ClientID)
	params.Set("redirect_uri", copilot.RedirectURI)
	params.Set("scope", copilot.Scopes)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", state)

	authURL := fmt.Sprintf("%s?%s", copilot.AuthorizeURL, params.Encode())

	c.JSON(http.StatusOK, StartCopilotOAuthResponse{SessionID: state, AuthURL: authURL})
}

type ExchangeCopilotOAuthRequest struct {
	SessionID   string `json:"session_id" binding:"required"`
	CallbackURL string `json:"callback_url" binding:"required"`
}

type ExchangeCopilotOAuthResponse struct {
	Credentials string `json:"credentials"`
}

func parseCopilotCallbackURL(callbackURL string) (string, string, error) {
	trimmed := strings.TrimSpace(callbackURL)
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return "", "", fmt.Errorf("callback_url must be a full URL")
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("invalid callback_url: %w", err)
	}

	q := u.Query()

	code := q.Get("code")
	if code == "" {
		return "", "", fmt.Errorf("code parameter not found in callback_url")
	}

	state := q.Get("state")
	if state == "" {
		return "", "", fmt.Errorf("state parameter not found in callback_url")
	}

	return code, state, nil
}

func (h *CopilotHandlers) Exchange(c *gin.Context) {
	ctx := c.Request.Context()

	var req ExchangeCopilotOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("invalid request format"))
		return
	}

	cacheKey := copilotOAuthCacheKey(req.SessionID)

	state, err := h.stateCache.Get(ctx, cacheKey)
	if err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("invalid or expired oauth session"))
		return
	}

	if err := h.stateCache.Delete(ctx, cacheKey); err != nil {
		log.Warn(ctx, "failed to delete used oauth state from cache", log.String("session_id", req.SessionID), log.Cause(err))
	}

	code, callbackState, err := parseCopilotCallbackURL(req.CallbackURL)
	if err != nil {
		JSONError(c, http.StatusBadRequest, err)
		return
	}

	if callbackState != req.SessionID {
		JSONError(c, http.StatusBadRequest, errors.New("oauth state mismatch"))
		return
	}

	tokenProvider := copilot.NewOAuthTokenProvider(oauth.TokenProviderParams{
		HTTPClient: h.httpClient,
	})

	creds, err := tokenProvider.Exchange(ctx, oauth.ExchangeParams{
		Code:         code,
		CodeVerifier: state.CodeVerifier,
		ClientID:     copilot.ClientID,
		RedirectURI:  copilot.RedirectURI,
	})
	if err != nil {
		JSONError(c, http.StatusBadGateway, fmt.Errorf("token exchange failed: %w", err))
		return
	}

	creds = copilot.NormalizeOAuthCredentials(creds)

	output, err := creds.ToJSON()
	if err != nil {
		JSONError(c, http.StatusInternalServerError, fmt.Errorf("failed to encode credentials: %w", err))
		return
	}

	c.JSON(http.StatusOK, ExchangeCopilotOAuthResponse{Credentials: output})
}
