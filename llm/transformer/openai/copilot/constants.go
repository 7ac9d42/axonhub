package copilot

import "github.com/looplj/axonhub/llm/oauth"

func DefaultModels() []string {
	return []string{
		"gpt-4.1",
		"gpt-4o",
		"o3-mini",
		"claude-3.5-sonnet",
		"claude-3.7-sonnet",
		"openai/gpt-4.1",
		"openai/gpt-4o",
		"openai/o3-mini",
		"anthropic/claude-3.5-sonnet",
		"anthropic/claude-3.7-sonnet",
	}
}

const (
	AuthorizeURL = "https://github.com/login/oauth/authorize"
	//nolint:gosec
	TokenURL = "https://github.com/login/oauth/access_token"
	//nolint:gosec
	CopilotTokenURL = "https://api.github.com/copilot_internal/v2/token"
	//nolint:gosec
	ClientID = "Iv1.b507a08c87ecfe98"

	RedirectURI = "http://localhost:1455/auth/callback"
	Scopes      = "read:user"

	BaseURL = "https://api.githubcopilot.com"

	UserAgent           = "GitHubCopilotChat/0.26.7"
	EditorVersion       = "vscode/1.96.2"
	EditorPluginVersion = "copilot-chat/0.26.7"
	IntegrationID       = "vscode-chat"
)

var DefaultTokenURLs = oauth.OAuthUrls{
	AuthorizeUrl: AuthorizeURL,
	TokenUrl:     TokenURL,
}
