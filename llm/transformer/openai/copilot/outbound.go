package copilot

import (
	"context"
	"fmt"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

type Params struct {
	TokenProvider oauth.TokenGetter
	BaseURL       string
}

type OutboundTransformer struct {
	tokens oauth.TokenGetter
	base   transformer.Outbound
}

func NewOutboundTransformer(params Params) (*OutboundTransformer, error) {
	if params.TokenProvider == nil {
		return nil, fmt.Errorf("token provider is required")
	}

	baseURL := strings.TrimSpace(params.BaseURL)
	if baseURL == "" {
		baseURL = BaseURL
	}

	openAIBaseURL := strings.TrimSuffix(baseURL, "#") + "#"
	base, err := openai.NewOutboundTransformer(openAIBaseURL, "dummy")
	if err != nil {
		return nil, err
	}

	return &OutboundTransformer{
		tokens: params.TokenProvider,
		base:   base,
	}, nil
}

func (t *OutboundTransformer) APIFormat() llm.APIFormat {
	return t.base.APIFormat()
}

func (t *OutboundTransformer) TransformError(ctx context.Context, rawErr *httpclient.Error) *llm.ResponseError {
	return t.base.TransformError(ctx, rawErr)
}

func (t *OutboundTransformer) TransformRequest(ctx context.Context, llmReq *llm.Request) (*httpclient.Request, error) {
	hreq, err := t.base.TransformRequest(ctx, llmReq)
	if err != nil {
		return nil, err
	}

	creds, err := t.tokens.Get(ctx)
	if err != nil {
		return nil, err
	}

	hreq.Auth = &httpclient.AuthConfig{
		Type:   httpclient.AuthTypeBearer,
		APIKey: creds.AccessToken,
	}

	hreq.Headers.Set("User-Agent", UserAgent)
	hreq.Headers.Set("Editor-Version", EditorVersion)
	hreq.Headers.Set("Editor-Plugin-Version", EditorPluginVersion)
	hreq.Headers.Set("Copilot-Integration-Id", IntegrationID)

	if hreq.Headers.Get("Accept") == "" {
		if llmReq.Stream != nil && *llmReq.Stream {
			hreq.Headers.Set("Accept", "text/event-stream")
		} else {
			hreq.Headers.Set("Accept", "application/json")
		}
	}

	return hreq, nil
}

func (t *OutboundTransformer) TransformResponse(ctx context.Context, httpResp *httpclient.Response) (*llm.Response, error) {
	return t.base.TransformResponse(ctx, httpResp)
}

func (t *OutboundTransformer) TransformStream(ctx context.Context, streamIn streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	return t.base.TransformStream(ctx, streamIn)
}

func (t *OutboundTransformer) AggregateStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return t.base.AggregateStreamChunks(ctx, chunks)
}
