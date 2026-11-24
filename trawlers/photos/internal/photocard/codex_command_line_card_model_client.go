package photocard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// CodexCommandLineCardModelClient runs one `codex exec` per photo, signed in
// with the person's ChatGPT account. Codex is an agent, so it runs with no
// configuration of the person's own, a read-only sandbox and an empty working
// folder, and keeps no session.
type CodexCommandLineCardModelClient struct {
	cardModelName        string
	cardModelEffort      string
	chatGPTLoginVerified bool
}

func NewCodexCommandLineCardModelClient(cardModelName, cardModelEffort string) *CodexCommandLineCardModelClient {
	return &CodexCommandLineCardModelClient{
		cardModelName:   cardModelName,
		cardModelEffort: strings.ToLower(strings.TrimSpace(cardModelEffort)),
	}
}

func (*CodexCommandLineCardModelClient) CardModelProvider() photo.CardModelProvider {
	return photo.CardModelProvider_CARD_MODEL_PROVIDER_CODEX_COMMAND_LINE
}

func (client *CodexCommandLineCardModelClient) CardModelName() string { return client.cardModelName }
func (client *CodexCommandLineCardModelClient) CardModelEffort() string {
	return client.cardModelEffort
}

// One line of `codex exec --json`, at its boundary.
type codexExecEvent struct {
	Type string `json:"type"`
	Item struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Usage struct {
		InputTokens           int64 `json:"input_tokens"`
		OutputTokens          int64 `json:"output_tokens"`
		ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	Message string `json:"message"`
}

func (client *CodexCommandLineCardModelClient) verifyChatGPTSubscriptionLogin(ctx context.Context) error {
	if client.chatGPTLoginVerified {
		return nil
	}
	if _, err := exec.LookPath("codex"); err != nil {
		return &CardModelRefusedAccessError{CardModelRefusalMessage: "codex command was not found in PATH."}
	}
	loginOutput, err := exec.CommandContext(ctx, "codex", "login", "status").CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	statusText := strings.TrimSpace(string(loginOutput))
	if err != nil || !strings.Contains(strings.ToLower(statusText), "chatgpt") {
		if statusText == "" && err != nil {
			statusText = err.Error()
		}
		return &CardModelRefusedAccessError{CardModelRefusalMessage: "codex is not logged in with a ChatGPT subscription (" + statusText + ")."}
	}
	client.chatGPTLoginVerified = true
	return nil
}

func codexFailureRefusesAccess(failureMessage string) bool {
	lower := strings.ToLower(failureMessage)
	for _, marker := range []string{
		"unauthorized", "forbidden", "401", "403", "429",
		"not logged in", "login required", "authentication", "invalid_api_key",
		"quota", "usage limit", "rate limit", "billing", "model_not_found", "does not exist",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (client *CodexCommandLineCardModelClient) AnswerPhotoCardModelInput(ctx context.Context, photoCardModelInputText string, pictureJPEGFilePath string) (CardModelAnswer, error) {
	if err := client.verifyChatGPTSubscriptionLogin(ctx); err != nil {
		return CardModelAnswer{}, err
	}
	emptyWorkingFolder, err := os.MkdirTemp("", "opentrawl-photo-card-codex-")
	if err != nil {
		return CardModelAnswer{}, fmt.Errorf("make an empty folder for Codex: %w", err)
	}
	defer func() { _ = os.RemoveAll(emptyWorkingFolder) }()
	callContext, cancel := context.WithTimeout(ctx, cardModelCallTimeout)
	defer cancel()
	codexArgs := []string{
		"exec",
		"--ephemeral", "--skip-git-repo-check", "--ignore-user-config", "--ignore-rules",
		"--sandbox", "read-only", "--cd", emptyWorkingFolder,
		"--model", client.cardModelName,
	}
	if client.cardModelEffort != "" {
		codexArgs = append(codexArgs, "-c", "model_reasoning_effort="+client.cardModelEffort)
	}
	codexArgs = append(codexArgs, "--image", pictureJPEGFilePath, "--json", "-")
	codexCommand := exec.CommandContext(callContext, "codex", codexArgs...)
	codexCommand.Stdin = strings.NewReader(photoCardModelInputText)
	var standardOutput, standardError bytes.Buffer
	codexCommand.Stdout = &standardOutput
	codexCommand.Stderr = &standardError
	runErr := codexCommand.Run()
	if ctx.Err() != nil {
		return CardModelAnswer{}, ctx.Err()
	}
	var answer CardModelAnswer
	var codexFailureMessages []string
	scanner := bufio.NewScanner(&standardOutput)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		var event codexExecEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		switch event.Type {
		case "item.completed":
			if event.Item.Type == "agent_message" {
				answer.RawModelAnswer = event.Item.Text
			}
		case "turn.completed":
			answer.InputTokens = &event.Usage.InputTokens
			answer.OutputTokens = &event.Usage.OutputTokens
			answer.ReasoningTokens = &event.Usage.ReasoningOutputTokens
		case "turn.failed":
			codexFailureMessages = append(codexFailureMessages, event.Error.Message)
		case "error":
			codexFailureMessages = append(codexFailureMessages, event.Message)
		}
	}
	if runErr != nil || answer.RawModelAnswer == "" {
		failureMessage := strings.TrimSpace(strings.Join(codexFailureMessages, "; ") + " " + strings.TrimSpace(standardError.String()))
		if runErr != nil {
			failureMessage = strings.TrimSpace("codex exec failed (" + runErr.Error() + "): " + failureMessage)
		}
		if errors.Is(runErr, exec.ErrNotFound) || codexFailureRefusesAccess(failureMessage) {
			return CardModelAnswer{}, &CardModelRefusedAccessError{CardModelRefusalMessage: failureMessage}
		}
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: failureMessage}
	}
	return answer, nil
}
