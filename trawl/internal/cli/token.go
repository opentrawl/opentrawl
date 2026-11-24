package cli

import (
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// TokenCmd manages the scoped access tokens agents use with trawl mcp.
type TokenCmd struct {
	Create TokenCreateCmd `cmd:"" help:"Create a read-only token for one agent, limited to chosen sources"`
	List   TokenListCmd   `cmd:"" default:"withargs" help:"List tokens, newest first"`
	Revoke TokenRevokeCmd `cmd:"" help:"Revoke a token so agents using it are refused"`
}

type TokenCreateCmd struct {
	Agent   string `arg:"" name:"agent" help:"Name of the agent that will use the token"`
	Sources string `name:"sources" default:"all" placeholder:"TRAWLERS" help:"Sources the token may read, separated by commas, or all"`
	Relay   bool   `name:"relay" help:"Allow this manual token on the public --relay HTTPS endpoint (default: local and Tailscale only)"`
}

type TokenListCmd struct{}

type TokenRevokeCmd struct {
	Token string `arg:"" name:"token-id" help:"Token id from trawl token list, such as tok_1a2b3c4d"`
}

func agentAccessTokenRowPresentation(token agentAccessTokenRecord) render.AgentAccessTokenRowPresentation {
	return render.AgentAccessTokenRowPresentation{
		AgentAccessTokenIdentifier: string(token.AgentAccessTokenIdentifier),
		AgentName:                  token.AgentName,
		AllowedSourcesText:         token.allowedSourcesDisplayText(),
		CreatedDate:                token.CreatedTime.Local().Format("2006-01-02"),
		AgentAccessTokenRevoked:    token.RevokedTime != nil,
	}
}

func (c *TokenCreateCmd) Run(r *Runtime) error {
	if strings.TrimSpace(c.Agent) == "" {
		return render.AgentAccessCommandIsWrong("Name the agent, such as trawl token create claude-code")
	}
	allSources, requestedSources := parseAllowedSourcesList(c.Sources)
	if !allSources {
		installed := discoverInstalledTrawlers(r.ctx)
		if _, err := r.selectInstalledTrawlers(installed, requestedSources); err != nil {
			return err
		}
	}
	surface := agentAccessTokenSurfaceLocal
	if c.Relay {
		surface = agentAccessTokenSurfaceRelay
	}
	token, bearerToken, err := createAgentAccessTokenForSurface(r.stateRoot, c.Agent, c.Sources, surface, time.Now())
	if err != nil {
		return render.AgentAccessOperationFailed("Cannot save the token: " + err.Error())
	}
	return r.output.WriteAgentAccessTokenCreated(agentAccessTokenRowPresentation(token), bearerToken)
}

func (c *TokenListCmd) Run(r *Runtime) error {
	tokens, err := loadAgentAccessTokens(r.stateRoot)
	if err != nil {
		return render.AgentAccessOperationFailed("Cannot read tokens: " + err.Error())
	}
	sort.SliceStable(tokens, func(i, j int) bool { return tokens[i].CreatedTime.After(tokens[j].CreatedTime) })
	rows := make([]render.AgentAccessTokenRowPresentation, 0, len(tokens))
	for _, token := range tokens {
		rows = append(rows, agentAccessTokenRowPresentation(token))
	}
	return r.output.WriteAgentAccessTokenList(rows)
}

func (c *TokenRevokeCmd) Run(r *Runtime) error {
	found, err := revokeAgentAccessToken(r.stateRoot, AgentAccessTokenIdentifier(strings.TrimSpace(c.Token)), time.Now())
	if err != nil {
		return render.AgentAccessOperationFailed("Cannot revoke the token: " + err.Error())
	}
	if !found {
		return render.AgentAccessCommandIsWrong("No token has id " + c.Token + "; see trawl token list")
	}
	return r.output.WriteAgentAccessTokenRevoked(c.Token)
}
