package copilot

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/oauth"
)

type staticTokenGetter struct {
	token string
}

func (s staticTokenGetter) Get(_ context.Context) (*oauth.OAuthCredentials, error) {
	return &oauth.OAuthCredentials{AccessToken: s.token}, nil
}

func TestOutboundTransformer_TransformRequest_OfficialURL(t *testing.T) {
	transformer, err := NewOutboundTransformer(Params{
		TokenProvider: staticTokenGetter{token: "copilot-token"},
		BaseURL:       BaseURL,
	})
	require.NoError(t, err)

	message := "hello"
	httpReq, err := transformer.TransformRequest(context.Background(), &llm.Request{
		Model: "gpt-4o",
		Messages: []llm.Message{{
			Role: "user",
			Content: llm.MessageContent{
				Content: &message,
			},
		}},
	})
	require.NoError(t, err)

	assert.Equal(t, BaseURL+"/chat/completions", httpReq.URL)
	assert.NotContains(t, httpReq.URL, "/v1/")
	assert.Equal(t, "copilot-token", httpReq.Auth.APIKey)
	assert.Equal(t, UserAgent, httpReq.Headers.Get("User-Agent"))
}

func TestOutboundTransformer_TransformRequest_CustomBaseURL(t *testing.T) {
	transformer, err := NewOutboundTransformer(Params{
		TokenProvider: staticTokenGetter{token: "copilot-token"},
		BaseURL:       "https://example.com/copilot/",
	})
	require.NoError(t, err)

	message := "hello"
	httpReq, err := transformer.TransformRequest(context.Background(), &llm.Request{
		Model: "gpt-4o",
		Messages: []llm.Message{{
			Role: "user",
			Content: llm.MessageContent{
				Content: &message,
			},
		}},
	})
	require.NoError(t, err)

	assert.Equal(t, "https://example.com/copilot/chat/completions", httpReq.URL)
	assert.False(t, strings.Contains(httpReq.URL, "/v1"))
}
