package photocard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// ClaudeCommandLineCardModelClient runs one stripped `claude -p` call per photo
// using the person's signed-in Claude account (respecting CLAUDE_CONFIG_DIR
// when set). The picture is sent inline via stream-json with all tools, slash
// commands and MCP servers disabled so each photo takes one turn.
type ClaudeCommandLineCardModelClient struct {
	cardModelName   string
	cardModelEffort string
}

func NewClaudeCommandLineCardModelClient(cardModelName, cardModelEffort string) *ClaudeCommandLineCardModelClient {
	return &ClaudeCommandLineCardModelClient{
		cardModelName:   cardModelName,
		cardModelEffort: strings.ToLower(strings.TrimSpace(cardModelEffort)),
	}
}

func (*ClaudeCommandLineCardModelClient) CardModelProvider() photo.CardModelProvider {
	return photo.CardModelProvider_CARD_MODEL_PROVIDER_CLAUDE_COMMAND_LINE
}

func (client *ClaudeCommandLineCardModelClient) CardModelName() string { return client.cardModelName }
func (client *ClaudeCommandLineCardModelClient) CardModelEffort() string {
	return client.cardModelEffort
}

type claudeStreamJSONEvent struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	Usage struct {
		InputTokens              int64 `json:"input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		OutputTokensDetails      struct {
			ThinkingTokens *int64 `json:"thinking_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
	Errors []string `json:"errors"`
}

func claudeCLIFailureRefusesAccess(failureMessage string) bool {
	lower := strings.ToLower(failureMessage)
	for _, marker := range []string{
		"not logged in", "please log in", "login required", "authentication",
		"unauthorized", "forbidden", "401", "403", "429",
		"hit your limit", "usage limit", "rate limit", "quota", "credit balance",
		"billing", "does not exist", "model_not_found",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (client *ClaudeCommandLineCardModelClient) AnswerPhotoCardModelInput(ctx context.Context, photoCardModelInputText string, pictureJPEGFilePath string) (CardModelAnswer, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return CardModelAnswer{}, &CardModelRefusedAccessError{CardModelRefusalMessage: "claude command was not found in PATH."}
	}
	pictureJPEGBytes, err := os.ReadFile(pictureJPEGFilePath)
	if err != nil {
		return CardModelAnswer{}, fmt.Errorf("read picture for the card model: %w", err)
	}
	inputPayload, err := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{
				{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": "image/jpeg",
						"data":       base64.StdEncoding.EncodeToString(pictureJPEGBytes),
					},
				},
				{
					"type": "text",
					"text": photoCardModelInputText,
				},
			},
		},
	})
	if err != nil {
		return CardModelAnswer{}, err
	}
	inputPayload = append(inputPayload, '\n')

	emptyWorkingFolder, err := os.MkdirTemp("", "opentrawl-photo-card-claude-")
	if err != nil {
		return CardModelAnswer{}, fmt.Errorf("make an empty folder for Claude: %w", err)
	}
	defer func() { _ = os.RemoveAll(emptyWorkingFolder) }()

	callContext, cancel := context.WithTimeout(ctx, cardModelCallTimeout)
	defer cancel()

	claudeArgs := []string{
		"-p",
		"--model", client.cardModelName,
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--system-prompt", "",
		"--tools", "",
		"--disable-slash-commands",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--no-session-persistence",
	}
	if client.cardModelEffort != "" {
		claudeArgs = append(claudeArgs, "--effort", client.cardModelEffort)
	}
	claudeCommand := exec.CommandContext(callContext, "claude", claudeArgs...)
	claudeCommand.Dir = emptyWorkingFolder
	claudeCommand.Stdin = bytes.NewReader(inputPayload)
	var standardOutput, standardError bytes.Buffer
	claudeCommand.Stdout = &standardOutput
	claudeCommand.Stderr = &standardError

	runErr := claudeCommand.Run()
	if ctx.Err() != nil {
		return CardModelAnswer{}, ctx.Err()
	}

	var answer CardModelAnswer
	var assistantTextParts []string
	var failureMessages []string
	resultIsError := false

	scanner := bufio.NewScanner(&standardOutput)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var event claudeStreamJSONEvent
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		switch event.Type {
		case "assistant":
			for _, block := range event.Message.Content {
				if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
					assistantTextParts = append(assistantTextParts, block.Text)
				}
			}
		case "result":
			if event.IsError {
				resultIsError = true
				if strings.TrimSpace(event.Result) != "" {
					failureMessages = append(failureMessages, strings.TrimSpace(event.Result))
				}
				failureMessages = append(failureMessages, event.Errors...)
			} else if strings.TrimSpace(event.Result) != "" {
				answer.RawModelAnswer = strings.TrimSpace(event.Result)
			}
			totalInput := event.Usage.InputTokens + event.Usage.CacheCreationInputTokens + event.Usage.CacheReadInputTokens
			if totalInput > 0 {
				answer.InputTokens = new(totalInput)
			}
			if event.Usage.OutputTokens > 0 {
				answer.OutputTokens = new(event.Usage.OutputTokens)
			}
			if event.Usage.OutputTokensDetails.ThinkingTokens != nil {
				answer.ReasoningTokens = new(*event.Usage.OutputTokensDetails.ThinkingTokens)
			}
		}
	}
	if answer.RawModelAnswer == "" && !resultIsError && len(assistantTextParts) > 0 {
		answer.RawModelAnswer = strings.TrimSpace(strings.Join(assistantTextParts, "\n"))
	}
	if runErr != nil || resultIsError || answer.RawModelAnswer == "" {
		failureMessage := strings.TrimSpace(strings.Join(failureMessages, "; ") + " " + strings.TrimSpace(standardError.String()))
		if runErr != nil {
			failureMessage = strings.TrimSpace("claude -p failed (" + runErr.Error() + "): " + failureMessage)
		}
		if failureMessage == "" {
			failureMessage = "claude -p returned an empty answer."
		}
		if errors.Is(runErr, exec.ErrNotFound) || claudeCLIFailureRefusesAccess(failureMessage) {
			return CardModelAnswer{}, &CardModelRefusedAccessError{CardModelRefusalMessage: failureMessage}
		}
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: failureMessage}
	}
	return answer, nil
}
