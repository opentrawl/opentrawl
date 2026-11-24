package photocard

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// AntigravityACPCardModelClient keeps one `agy acp --stdio` server running for
// the step and opens a fresh isolated ACP session for each photo, sending the
// upright JPEG inline as an ACP image content block.
type AntigravityACPCardModelClient struct {
	cardModelName   string
	cardModelEffort string

	mu                 sync.Mutex
	emptyWorkingFolder string
	acpCommand         *exec.Cmd
	acpStdin           io.WriteCloser
	acpStdout          *bufio.Scanner
	nextRequestID      int64
}

func NewAntigravityACPCardModelClient(cardModelName, cardModelEffort string) *AntigravityACPCardModelClient {
	return &AntigravityACPCardModelClient{
		cardModelName:   cardModelName,
		cardModelEffort: strings.ToLower(strings.TrimSpace(cardModelEffort)),
	}
}

func (*AntigravityACPCardModelClient) CardModelProvider() photo.CardModelProvider {
	return photo.CardModelProvider_CARD_MODEL_PROVIDER_ANTIGRAVITY_ACP
}

func (client *AntigravityACPCardModelClient) CardModelName() string   { return client.cardModelName }
func (client *AntigravityACPCardModelClient) CardModelEffort() string { return client.cardModelEffort }

// Close stops the persistent `agy acp` subprocess and removes its empty working
// folder.
func (client *AntigravityACPCardModelClient) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.stopServerLocked()
	if client.emptyWorkingFolder != "" {
		_ = os.RemoveAll(client.emptyWorkingFolder)
		client.emptyWorkingFolder = ""
	}
	return nil
}

func (client *AntigravityACPCardModelClient) stopServerLocked() {
	if client.acpStdin != nil {
		_ = client.acpStdin.Close()
		client.acpStdin = nil
	}
	if client.acpCommand != nil {
		if client.acpCommand.Process != nil {
			_ = client.acpCommand.Process.Kill()
		}
		_ = client.acpCommand.Wait()
		client.acpCommand = nil
	}
	client.acpStdout = nil
}

// JSON-RPC 2.0 wire types for Antigravity ACP over stdio, at its boundary.
type acpJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type acpJSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type acpJSONRPCMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *acpJSONRPCError `json:"error,omitempty"`
}

type acpConfigOptionValue struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

type acpConfigOption struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Category     string                 `json:"category"`
	CurrentValue string                 `json:"currentValue"`
	Options      []acpConfigOptionValue `json:"options"`
}

type acpSessionNewResult struct {
	SessionID     string            `json:"sessionId"`
	ConfigOptions []acpConfigOption `json:"configOptions"`
}

type acpSessionUpdateParams struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		SessionUpdate string `json:"sessionUpdate"`
		Content       struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *acpUsage `json:"usage,omitempty"`
	} `json:"update"`
}

type acpUsage struct {
	InputTokens        *int64 `json:"inputTokens,omitempty"`
	OutputTokens       *int64 `json:"outputTokens,omitempty"`
	ThoughtTokens      *int64 `json:"thoughtTokens,omitempty"`
	InputTokensSnake   *int64 `json:"input_tokens,omitempty"`
	OutputTokensSnake  *int64 `json:"output_tokens,omitempty"`
	ThoughtTokensSnake *int64 `json:"thought_tokens,omitempty"`
}

type acpPromptResult struct {
	StopReason string    `json:"stopReason"`
	Usage      *acpUsage `json:"usage,omitempty"`
}

func (usage *acpUsage) applyToAnswer(answer *CardModelAnswer) {
	if usage == nil {
		return
	}
	if usage.InputTokens != nil {
		answer.InputTokens = new(*usage.InputTokens)
	} else if usage.InputTokensSnake != nil {
		answer.InputTokens = new(*usage.InputTokensSnake)
	}
	if usage.OutputTokens != nil {
		answer.OutputTokens = new(*usage.OutputTokens)
	} else if usage.OutputTokensSnake != nil {
		answer.OutputTokens = new(*usage.OutputTokensSnake)
	}
	if usage.ThoughtTokens != nil {
		answer.ReasoningTokens = new(*usage.ThoughtTokens)
	} else if usage.ThoughtTokensSnake != nil {
		answer.ReasoningTokens = new(*usage.ThoughtTokensSnake)
	}
}

func (client *AntigravityACPCardModelClient) ensureServerStartedLocked(ctx context.Context) error {
	if client.acpCommand != nil {
		return nil
	}
	if _, err := exec.LookPath("agy"); err != nil {
		return &CardModelRefusedAccessError{CardModelRefusalMessage: "agy command was not found in PATH."}
	}
	if client.emptyWorkingFolder == "" {
		emptyFolder, err := os.MkdirTemp("", "opentrawl-photo-card-agy-")
		if err != nil {
			return fmt.Errorf("make an empty folder for Antigravity ACP: %w", err)
		}
		client.emptyWorkingFolder = emptyFolder
	}
	acpCommand := exec.Command("agy", "acp", "--stdio", "--include-default-customizations=false")
	acpCommand.Dir = client.emptyWorkingFolder
	acpCommand.Env = cleanEnvironmentForAntigravityACP(os.Environ())
	stdin, err := acpCommand.StdinPipe()
	if err != nil {
		return fmt.Errorf("open stdin for agy acp: %w", err)
	}
	stdout, err := acpCommand.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open stdout for agy acp: %w", err)
	}
	acpCommand.Stderr = io.Discard
	if err := acpCommand.Start(); err != nil {
		_ = stdin.Close()
		return &CardModelRefusedAccessError{CardModelRefusalMessage: "could not start agy acp: " + err.Error()}
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	client.acpCommand = acpCommand
	client.acpStdin = stdin
	client.acpStdout = scanner
	client.nextRequestID = 0

	initParams := map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo": map[string]string{
			"name":    "opentrawl-photos",
			"version": "1",
		},
	}
	if _, err := client.callRPCLocked(ctx, "initialize", initParams, nil); err != nil {
		client.stopServerLocked()
		return err
	}
	return nil
}

func cleanEnvironmentForAntigravityACP(environment []string) []string {
	cleaned := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, hasEquals := strings.Cut(entry, "=")
		if !hasEquals {
			continue
		}
		// Strip nested agent overrides and SSH session markers so `agy acp`
		// always reads the local macOS Keychain login instead of switching to
		// an isolated T3 directory or SSH file-storage mode.
		if key == "GEMINI_HOME" || key == "BROWSER" ||
			key == "SSH_CONNECTION" || key == "SSH_CLIENT" || key == "SSH_TTY" ||
			strings.HasPrefix(key, "ANTIGRAVITY_") ||
			strings.HasPrefix(key, "AGY_") {
			continue
		}
		cleaned = append(cleaned, entry)
	}
	return cleaned
}

func (client *AntigravityACPCardModelClient) writeJSONLineLocked(payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = client.acpStdin.Write(encoded)
	return err
}

func (client *AntigravityACPCardModelClient) callRPCLocked(
	ctx context.Context,
	method string,
	params any,
	onNotification func(method string, params json.RawMessage),
) (json.RawMessage, error) {
	client.nextRequestID++
	requestID := client.nextRequestID
	if err := client.writeJSONLineLocked(acpJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  method,
		Params:  params,
	}); err != nil {
		return nil, &CardModelCallFailedError{CardModelCallFailureMessage: "Antigravity ACP write failed: " + err.Error()}
	}

	type readResult struct {
		line []byte
		ok   bool
		err  error
	}
	for {
		lineCh := make(chan readResult, 1)
		go func() {
			ok := client.acpStdout.Scan()
			var lineCopy []byte
			if ok {
				lineCopy = append([]byte(nil), client.acpStdout.Bytes()...)
			}
			lineCh <- readResult{line: lineCopy, ok: ok, err: client.acpStdout.Err()}
		}()
		var res readResult
		select {
		case <-ctx.Done():
			client.stopServerLocked()
			return nil, ctx.Err()
		case res = <-lineCh:
		}
		if !res.ok {
			errText := "unexpected EOF"
			if res.err != nil {
				errText = res.err.Error()
			}
			client.stopServerLocked()
			return nil, &CardModelCallFailedError{CardModelCallFailureMessage: "Antigravity ACP closed the connection: " + errText}
		}
		trimmed := strings.TrimSpace(string(res.line))
		if trimmed == "" || trimmed[0] != '{' {
			continue
		}
		var message acpJSONRPCMessage
		if err := json.Unmarshal([]byte(trimmed), &message); err != nil {
			continue
		}
		// Server-to-client request: for permission requests, select a reject
		// option so the model continues and answers from the image without
		// running tools. Any other client request is declined cleanly.
		if message.Method != "" && message.ID != nil {
			if message.Method == "session/request_permission" {
				_ = client.writeJSONLineLocked(acpPermissionRefusalResponse(*message.ID, message.Params))
			} else {
				_ = client.writeJSONLineLocked(map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(*message.ID),
					"error": map[string]any{
						"code":    -32601,
						"message": "Photo carding does not allow client tool requests.",
					},
				})
			}
			continue
		}
		// Server-to-client notification.
		if message.Method != "" && message.ID == nil {
			if onNotification != nil {
				onNotification(message.Method, message.Params)
			}
			continue
		}
		// Response to our request.
		if message.ID == nil {
			continue
		}
		var responseID int64
		if err := json.Unmarshal(*message.ID, &responseID); err != nil || responseID != requestID {
			continue
		}
		if message.Error != nil {
			errMsg := strings.TrimSpace(message.Error.Message)
			if len(message.Error.Data) > 0 && string(message.Error.Data) != "null" {
				errMsg = strings.TrimSpace(errMsg + " " + string(message.Error.Data))
			}
			if acpErrorRefusesAccess(message.Error.Code, errMsg) {
				return nil, &CardModelRefusedAccessError{CardModelRefusalMessage: errMsg}
			}
			return nil, &CardModelCallFailedError{CardModelCallFailureMessage: "Antigravity ACP " + method + " failed: " + errMsg}
		}
		return message.Result, nil
	}
}

func acpPermissionRefusalResponse(requestID json.RawMessage, params json.RawMessage) map[string]any {
	var parsed struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if json.Unmarshal(params, &parsed) == nil {
		for _, option := range parsed.Options {
			if strings.HasPrefix(strings.ToLower(option.Kind), "reject") && option.OptionID != "" {
				return map[string]any{
					"jsonrpc": "2.0",
					"id":      requestID,
					"result": map[string]any{
						"outcome": map[string]any{
							"outcome":  "selected",
							"optionId": option.OptionID,
						},
					},
				}
			}
		}
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"result": map[string]any{
			"outcome": map[string]any{
				"outcome": "cancelled",
			},
		},
	}
}

func acpErrorRefusesAccess(errorCode int, errorMessage string) bool {
	if errorCode == -32000 {
		// ACP auth-required code often uses -32000.
		lower := strings.ToLower(errorMessage)
		if strings.Contains(lower, "auth") || strings.Contains(lower, "sign in") || strings.Contains(lower, "login") {
			return true
		}
	}
	lower := strings.ToLower(errorMessage)
	for _, marker := range []string{
		"not signed in", "please sign in", "login required", "unauthenticated", "authentication",
		"unauthorized", "forbidden", "401", "403", "429",
		"quota", "rate limit", "exhausted", "resource_exhausted", "billing", "model not found",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func matchACPConfigOptionValue(option acpConfigOption, requested string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(requested))
	for _, candidate := range option.Options {
		if strings.ToLower(strings.TrimSpace(candidate.Value)) == want ||
			strings.ToLower(strings.TrimSpace(candidate.Name)) == want {
			return candidate.Value, true
		}
	}
	return "", false
}

func isACPModelOption(option acpConfigOption) bool {
	category := strings.ToLower(option.Category)
	id := strings.ToLower(option.ID)
	name := strings.ToLower(option.Name)
	return category == "model" || id == "model" || name == "model"
}

func isACPEffortOption(option acpConfigOption) bool {
	category := strings.ToLower(option.Category)
	id := strings.ToLower(option.ID)
	name := strings.ToLower(option.Name)
	return category == "thought_level" ||
		strings.Contains(id, "thought") ||
		strings.Contains(id, "thinking") ||
		strings.Contains(id, "effort") ||
		strings.Contains(name, "thinking") ||
		strings.Contains(name, "effort")
}

func (client *AntigravityACPCardModelClient) AnswerPhotoCardModelInput(ctx context.Context, photoCardModelInputText string, pictureJPEGFilePath string) (CardModelAnswer, error) {
	pictureJPEGBytes, err := os.ReadFile(pictureJPEGFilePath)
	if err != nil {
		return CardModelAnswer{}, fmt.Errorf("read picture for the card model: %w", err)
	}
	callContext, cancel := context.WithTimeout(ctx, cardModelCallTimeout)
	defer cancel()

	client.mu.Lock()
	defer client.mu.Unlock()

	if err := client.ensureServerStartedLocked(callContext); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if ctx.Err() != nil {
				return CardModelAnswer{}, ctx.Err()
			}
			return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Antigravity ACP timed out starting."}
		}
		return CardModelAnswer{}, err
	}

	rawSessionNew, err := client.callRPCLocked(callContext, "session/new", map[string]any{
		"cwd":        client.emptyWorkingFolder,
		"mcpServers": []any{},
		"_meta": map[string]any{
			"antigravity": map[string]any{
				"systemPrompt": map[string]any{
					"append": "You are writing one photo card directly from the attached image and facts. Do not call any tools, do not run shell commands, do not read or write files, and do not search the web. Reply directly with the four requested sections.",
				},
			},
		},
	}, nil)
	if err != nil {
		if ctx.Err() != nil {
			return CardModelAnswer{}, ctx.Err()
		}
		return CardModelAnswer{}, err
	}
	var sessionNew acpSessionNewResult
	if err := json.Unmarshal(rawSessionNew, &sessionNew); err != nil || sessionNew.SessionID == "" {
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Antigravity ACP session/new returned no sessionId."}
	}
	defer func() {
		if client.acpCommand != nil && ctx.Err() == nil {
			_, _ = client.callRPCLocked(callContext, "session/close", map[string]any{
				"sessionId": sessionNew.SessionID,
			}, nil)
		}
	}()

	// Configure model first (changing model may update available effort options),
	// then configure effort if requested.
	activeOptions := sessionNew.ConfigOptions
	if client.cardModelName != "" {
		var matchedOption *acpConfigOption
		for i := range activeOptions {
			if isACPModelOption(activeOptions[i]) {
				matchedOption = &activeOptions[i]
				break
			}
		}
		if matchedOption != nil {
			targetValue, ok := matchACPConfigOptionValue(*matchedOption, client.cardModelName)
			if !ok {
				return CardModelAnswer{}, &CardModelRefusedAccessError{
					CardModelRefusalMessage: fmt.Sprintf("Antigravity ACP has no model %q.", client.cardModelName),
				}
			}
			if matchedOption.CurrentValue != targetValue {
				rawSetModel, err := client.callRPCLocked(callContext, "session/set_config_option", map[string]any{
					"sessionId": sessionNew.SessionID,
					"configId":  matchedOption.ID,
					"value":     targetValue,
				}, nil)
				if err != nil {
					if ctx.Err() != nil {
						return CardModelAnswer{}, ctx.Err()
					}
					return CardModelAnswer{}, err
				}
				var updated struct {
					ConfigOptions []acpConfigOption `json:"configOptions"`
				}
				if json.Unmarshal(rawSetModel, &updated) == nil && len(updated.ConfigOptions) > 0 {
					activeOptions = updated.ConfigOptions
				}
			}
		}
	}
	if client.cardModelEffort != "" {
		var matchedEffort *acpConfigOption
		for i := range activeOptions {
			if isACPEffortOption(activeOptions[i]) {
				matchedEffort = &activeOptions[i]
				break
			}
		}
		if matchedEffort != nil {
			targetValue, ok := matchACPConfigOptionValue(*matchedEffort, client.cardModelEffort)
			if !ok {
				return CardModelAnswer{}, &CardModelRefusedAccessError{
					CardModelRefusalMessage: fmt.Sprintf("Antigravity ACP has no effort %q for model %q.", client.cardModelEffort, client.cardModelName),
				}
			}
			if matchedEffort.CurrentValue != targetValue {
				if _, err := client.callRPCLocked(callContext, "session/set_config_option", map[string]any{
					"sessionId": sessionNew.SessionID,
					"configId":  matchedEffort.ID,
					"value":     targetValue,
				}, nil); err != nil {
					if ctx.Err() != nil {
						return CardModelAnswer{}, ctx.Err()
					}
					return CardModelAnswer{}, err
				}
			}
		}
	}

	var answerText strings.Builder
	var answer CardModelAnswer
	rawPromptResult, err := client.callRPCLocked(callContext, "session/prompt", map[string]any{
		"sessionId": sessionNew.SessionID,
		"prompt": []map[string]any{
			{
				"type":     "image",
				"mimeType": "image/jpeg",
				"data":     base64.StdEncoding.EncodeToString(pictureJPEGBytes),
			},
			{
				"type": "text",
				"text": photoCardModelInputText,
			},
		},
	}, func(method string, params json.RawMessage) {
		if method != "session/update" {
			return
		}
		var updateParams acpSessionUpdateParams
		if json.Unmarshal(params, &updateParams) != nil {
			return
		}
		switch updateParams.Update.SessionUpdate {
		case "agent_message_chunk":
			if updateParams.Update.Content.Type == "text" {
				answerText.WriteString(updateParams.Update.Content.Text)
			}
		case "usage_update":
			updateParams.Update.Usage.applyToAnswer(&answer)
		}
	})
	if err != nil {
		if ctx.Err() != nil {
			return CardModelAnswer{}, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Antigravity ACP timed out writing the photo card."}
		}
		return CardModelAnswer{}, err
	}
	var promptResult acpPromptResult
	if json.Unmarshal(rawPromptResult, &promptResult) == nil {
		promptResult.Usage.applyToAnswer(&answer)
	}
	answer.RawModelAnswer = strings.TrimSpace(answerText.String())
	if answer.RawModelAnswer == "" {
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Antigravity ACP returned an empty answer."}
	}
	return answer, nil
}
