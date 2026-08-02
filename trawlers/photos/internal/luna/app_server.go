// Package luna provides the OpenTrawl-owned boundary to GPT-5.6 Luna.
//
// Codex app-server owns ChatGPT authentication and token refresh. OpenTrawl
// sends typed requests over the stable app-server protocol and never reads or
// stores OAuth credentials.
package luna

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"sync"
)

const (
	ModelGPT56Luna = "gpt-5.6-luna"

	maximumProtocolMessageBytes = 16 << 20
)

type ImageMediaType string

const (
	ImageJPEG ImageMediaType = "image/jpeg"
	ImagePNG  ImageMediaType = "image/png"
	ImageWebP ImageMediaType = "image/webp"
)

type AccountKind string

const (
	AccountNone    AccountKind = "none"
	AccountChatGPT AccountKind = "chatgpt"
	AccountAPIKey  AccountKind = "apiKey"
)

var ErrChatGPTSignInRequired = errors.New("OpenTrawl needs ChatGPT sign-in")
var ErrClientTerminal = errors.New("Luna app-server client is terminal")
var ErrGenerationOutcomePending = errors.New("Luna generation outcome remains pending")
var ErrGenerationDidNotComplete = errors.New("Luna generation did not complete")

// StructuredOutputSchema is a validated JSON Schema at the external protocol
// boundary. The Photos DAG constructs each schema from the card Protobuf
// descriptors rather than maintaining handwritten wire contracts.
type StructuredOutputSchema struct {
	encodedJSON json.RawMessage
}

func NewStructuredOutputSchema(encodedJSON []byte) (StructuredOutputSchema, error) {
	if !json.Valid(encodedJSON) {
		return StructuredOutputSchema{}, errors.New("Luna structured output schema is not valid JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encodedJSON, &object); err != nil || len(object) == 0 {
		return StructuredOutputSchema{}, errors.New("Luna structured output schema must be a non-empty JSON object")
	}
	var schemaType string
	if err := json.Unmarshal(object["type"], &schemaType); err != nil || schemaType != "object" {
		return StructuredOutputSchema{}, errors.New("Luna structured output schema must describe an object")
	}
	return StructuredOutputSchema{encodedJSON: append(json.RawMessage(nil), encodedJSON...)}, nil
}

type Configuration struct {
	CodexExecutablePath   string
	EmptyWorkingDirectory string
	ClientVersion         string
	PrivateWireTranscript io.Writer
}

type Account struct {
	Kind AccountKind
}

type ChatGPTSignIn struct {
	LoginID string
	URL     *url.URL
}

type GenerationRequest struct {
	Instructions             string
	Image                    []byte
	ImageMediaType           ImageMediaType
	OutputSchema             StructuredOutputSchema
	RetainedThreadIdentifier string
	RetainedTurnIdentifier   string
	TransmissionStarted      func(threadIdentifier string) error
	TurnStarted              func(threadIdentifier, turnIdentifier string) error
	ResponseReceived         func(GenerationResult) error
}

type GenerationResult struct {
	ThreadID                string
	TurnID                  string
	RawStructuredOutputJSON []byte
	TokenUsage              *TokenUsage
}

type TokenUsage struct {
	InputTokens           int64
	CachedInputTokens     int64
	OutputTokens          int64
	ReasoningOutputTokens int64
	TotalTokens           int64
}

// Client owns one headless app-server process. It is an internal transport,
// not a second macOS app and not a Photos-library permission identity.
type Client struct {
	command       *exec.Cmd
	stdin         io.WriteCloser
	messages      <-chan protocolRead
	stderrTail    *boundedTail
	processCancel context.CancelFunc

	stateMu              sync.Mutex
	nextRequestID        uint64
	terminal             bool
	stdinClosed          bool
	waitOnce             sync.Once
	operationSlot        chan struct{}
	pendingNotifications []protocolMessage

	configuration Configuration
}

func Start(ctx context.Context, configuration Configuration) (*Client, error) {
	if strings.TrimSpace(configuration.CodexExecutablePath) == "" {
		return nil, errors.New("Codex executable path is required")
	}
	if strings.TrimSpace(configuration.EmptyWorkingDirectory) == "" {
		return nil, errors.New("an empty Luna working directory is required")
	}
	if strings.TrimSpace(configuration.ClientVersion) == "" {
		return nil, errors.New("OpenTrawl client version is required")
	}

	disabledMCPServerArguments, err := disabledMCPArguments(ctx, configuration.CodexExecutablePath)
	if err != nil {
		return nil, err
	}
	appServerArguments := []string{
		"app-server",
		"--stdio",
		"--disable",
		"apps",
		"--config",
		`shell_environment_policy.inherit="none"`,
		"--config",
		"mcp_servers={}",
	}
	appServerArguments = append(appServerArguments, disabledMCPServerArguments...)
	processContext, processCancel := context.WithCancel(context.Background())
	command := exec.CommandContext(processContext, configuration.CodexExecutablePath, appServerArguments...)
	stdin, err := command.StdinPipe()
	if err != nil {
		processCancel()
		return nil, fmt.Errorf("open Codex app-server input: %w", err)
	}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		processCancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("open Codex app-server output: %w", err)
	}
	stderrTail := &boundedTail{maximumBytes: 64 << 10}
	command.Stderr = stderrTail
	if err := command.Start(); err != nil {
		processCancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}

	protocolMessages := make(chan protocolRead, 1)
	go readProtocolMessages(stdoutPipe, protocolMessages)
	client := &Client{
		command:       command,
		stdin:         stdin,
		messages:      protocolMessages,
		stderrTail:    stderrTail,
		processCancel: processCancel,
		operationSlot: make(chan struct{}, 1),
		configuration: configuration,
	}
	client.operationSlot <- struct{}{}

	var initialized initializeResponse
	if err := client.callLocked(ctx, "initialize", initializeParameters{
		ClientInfo: clientInformation{
			Name:    "opentrawl",
			Title:   "OpenTrawl",
			Version: configuration.ClientVersion,
		},
	}, &initialized); err != nil {
		_ = client.terminateAndDrain()
		return nil, err
	}
	if err := client.sendLocked(protocolNotification{JSONRPC: "2.0", Method: "initialized"}); err != nil {
		_ = client.terminateAndDrain()
		return nil, err
	}
	return client, nil
}

func disabledMCPArguments(ctx context.Context, codexExecutablePath string) ([]string, error) {
	command := exec.CommandContext(ctx, codexExecutablePath, "mcp", "list", "--json")
	command.Stderr = io.Discard
	encodedServers, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read inherited Codex MCP configuration: %w", err)
	}
	var configuredServers []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(encodedServers, &configuredServers); err != nil {
		return nil, fmt.Errorf("decode inherited Codex MCP configuration: %w", err)
	}
	arguments := make([]string, 0, len(configuredServers)*2)
	for _, configuredServer := range configuredServers {
		if strings.TrimSpace(configuredServer.Name) == "" {
			return nil, errors.New("inherited Codex MCP configuration has an unnamed server")
		}
		if !isTOMLBareKey(configuredServer.Name) {
			return nil, fmt.Errorf("inherited Codex MCP server name %q cannot be safely disabled", configuredServer.Name)
		}
		arguments = append(
			arguments,
			"--config",
			"mcp_servers."+configuredServer.Name+".enabled=false",
		)
	}
	return arguments, nil
}

func isTOMLBareKey(value string) bool {
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return value != ""
}

func (client *Client) Account(ctx context.Context) (Account, error) {
	var account Account
	err := client.runOperation(ctx, func() error {
		var response getAccountResponse
		if err := client.callLocked(ctx, "account/read", getAccountParameters{}, &response); err != nil {
			return err
		}
		if response.Account == nil {
			account = Account{Kind: AccountNone}
			return nil
		}
		switch response.Account.Type {
		case string(AccountChatGPT):
			account = Account{Kind: AccountChatGPT}
		case string(AccountAPIKey):
			account = Account{Kind: AccountAPIKey}
		default:
			return fmt.Errorf("unsupported Codex account type %q", response.Account.Type)
		}
		return nil
	})
	return account, err
}

func (client *Client) BeginChatGPTSignIn(ctx context.Context) (ChatGPTSignIn, error) {
	var signIn ChatGPTSignIn
	err := client.runOperation(ctx, func() error {
		var response loginAccountResponse
		if err := client.callLocked(ctx, "account/login/start", loginAccountParameters{
			Type:                      string(AccountChatGPT),
			ApplicationBrand:          "chatgpt",
			CodexStreamlinedLogin:     false,
			UseHostedLoginSuccessPage: true,
		}, &response); err != nil {
			return err
		}
		if response.Type != string(AccountChatGPT) || response.LoginID == "" || response.AuthenticationURL == "" {
			return errors.New("Codex app-server returned an incomplete ChatGPT sign-in")
		}
		authenticationURL, err := url.Parse(response.AuthenticationURL)
		if err != nil {
			return fmt.Errorf("parse ChatGPT sign-in URL: %w", err)
		}
		if authenticationURL.Scheme != "https" || authenticationURL.Host == "" {
			return errors.New("Codex app-server returned an unsafe ChatGPT sign-in URL")
		}
		signIn = ChatGPTSignIn{LoginID: response.LoginID, URL: authenticationURL}
		return nil
	})
	return signIn, err
}

func (client *Client) WaitForChatGPTSignIn(ctx context.Context, loginID string) error {
	return client.runOperation(ctx, func() error {
		for {
			message, err := client.receiveLocked(ctx)
			if err != nil {
				return err
			}
			if message.Method != "account/login/completed" {
				if len(message.ID) != 0 {
					return fmt.Errorf("Codex app-server requested unsupported method %q", message.Method)
				}
				continue
			}
			var completed accountLoginCompletedNotification
			if err := json.Unmarshal(message.Params, &completed); err != nil {
				return fmt.Errorf("decode ChatGPT sign-in completion: %w", err)
			}
			if completed.LoginID != "" && completed.LoginID != loginID {
				continue
			}
			if !completed.Success {
				if completed.Error == "" {
					return errors.New("ChatGPT sign-in failed")
				}
				return fmt.Errorf("ChatGPT sign-in failed: %s", completed.Error)
			}
			return nil
		}
	})
}

func (client *Client) Generate(ctx context.Context, request GenerationRequest) (GenerationResult, error) {
	if strings.TrimSpace(request.Instructions) == "" {
		return GenerationResult{}, errors.New("Luna instructions are required")
	}
	if len(request.Image) == 0 {
		return GenerationResult{}, errors.New("Luna image is required")
	}
	switch request.ImageMediaType {
	case ImageJPEG, ImagePNG, ImageWebP:
	default:
		return GenerationResult{}, fmt.Errorf("unsupported Luna image media type %q", request.ImageMediaType)
	}
	if len(request.OutputSchema.encodedJSON) == 0 {
		return GenerationResult{}, errors.New("Luna output schema is required")
	}
	if client.configuration.PrivateWireTranscript == nil {
		return GenerationResult{}, errors.New("a private Luna wire transcript is required")
	}
	var result GenerationResult
	err := client.runOperation(ctx, func() error {
		var generationError error
		result, generationError = client.generateLocked(ctx, request)
		return generationError
	})
	return result, err
}

func (client *Client) generateLocked(ctx context.Context, request GenerationRequest) (GenerationResult, error) {
	var accountResponse getAccountResponse
	if err := client.callLocked(ctx, "account/read", getAccountParameters{RefreshToken: true}, &accountResponse); err != nil {
		return GenerationResult{}, err
	}
	if accountResponse.Account == nil || accountResponse.Account.Type != string(AccountChatGPT) {
		return GenerationResult{}, ErrChatGPTSignInRequired
	}

	threadIdentifier := strings.TrimSpace(request.RetainedThreadIdentifier)
	turnIdentifier := strings.TrimSpace(request.RetainedTurnIdentifier)
	if threadIdentifier != "" {
		recovered, found, err := client.readCompletedGenerationLocked(ctx, threadIdentifier, turnIdentifier)
		if err != nil {
			if turnIdentifier == "" && recovered.TurnID != "" && request.TurnStarted != nil {
				if retainErr := request.TurnStarted(threadIdentifier, recovered.TurnID); retainErr != nil {
					return recovered, fmt.Errorf("%w: retain recovered Luna turn identifier: %v", ErrGenerationOutcomePending, retainErr)
				}
			}
			if errors.Is(err, ErrGenerationDidNotComplete) {
				if deleteErr := client.deleteThreadLocked(ctx, threadIdentifier); deleteErr != nil {
					return recovered, errors.Join(err, fmt.Errorf("delete failed Luna thread: %w", deleteErr))
				}
				return recovered, err
			}
			return recovered, fmt.Errorf("%w: %v", ErrGenerationOutcomePending, err)
		}
		if found {
			if request.ResponseReceived != nil {
				if err := request.ResponseReceived(recovered); err != nil {
					return GenerationResult{}, err
				}
			}
			if err := client.deleteThreadLocked(ctx, threadIdentifier); err != nil {
				return recovered, fmt.Errorf("delete retained Luna thread: %w", err)
			}
			return recovered, nil
		}
		if turnIdentifier != "" {
			return GenerationResult{ThreadID: threadIdentifier, TurnID: turnIdentifier}, ErrGenerationOutcomePending
		}
	} else {
		var threadResponse threadStartResponse
		if err := client.callLocked(ctx, "thread/start", threadStartParameters{
			ApprovalPolicy:   "never",
			BaseInstructions: "Return only the requested structured result. Do not inspect files, run commands, use tools, browse, or ask questions.",
			WorkingDirectory: client.configuration.EmptyWorkingDirectory,
			Ephemeral:        false,
			Model:            ModelGPT56Luna,
			Sandbox:          "read-only",
		}, &threadResponse); err != nil {
			return GenerationResult{}, err
		}
		threadIdentifier = threadResponse.Thread.ID
		if threadIdentifier == "" {
			return GenerationResult{}, errors.New("Codex app-server started a Luna thread without an identifier")
		}
		if request.TransmissionStarted != nil {
			if err := request.TransmissionStarted(threadIdentifier); err != nil {
				return GenerationResult{ThreadID: threadIdentifier}, err
			}
		}
	}

	imageDataURL := "data:" + string(request.ImageMediaType) + ";base64," + base64.StdEncoding.EncodeToString(request.Image)
	var turnResponse turnStartResponse
	if err := client.callLocked(ctx, "turn/start", turnStartParameters{
		ThreadID: threadIdentifier,
		Input: []turnInput{
			{Type: "text", Text: request.Instructions},
			{Type: "image", URL: imageDataURL, Detail: "original"},
		},
		ApprovalPolicy: "never",
		Model:          ModelGPT56Luna,
		OutputSchema:   request.OutputSchema.encodedJSON,
		SandboxPolicy: readOnlySandboxPolicy{
			Type:          "readOnly",
			NetworkAccess: false,
		},
	}, &turnResponse); err != nil {
		return GenerationResult{ThreadID: threadIdentifier}, fmt.Errorf("%w: %v", ErrGenerationOutcomePending, err)
	}
	turnIdentifier = turnResponse.Turn.ID
	if turnIdentifier == "" {
		return GenerationResult{ThreadID: threadIdentifier}, fmt.Errorf("%w: Codex app-server started a Luna turn without an identifier", ErrGenerationOutcomePending)
	}
	if request.TurnStarted != nil {
		if err := request.TurnStarted(threadIdentifier, turnIdentifier); err != nil {
			return GenerationResult{ThreadID: threadIdentifier, TurnID: turnIdentifier}, fmt.Errorf("%w: retain Luna turn identifier: %v", ErrGenerationOutcomePending, err)
		}
	}

	var finalAssistantMessage string
	var finalTokenUsage *TokenUsage
	for {
		message, err := client.receiveLocked(ctx)
		if err != nil {
			return GenerationResult{ThreadID: threadIdentifier, TurnID: turnIdentifier}, fmt.Errorf("%w: receive Luna result: %v", ErrGenerationOutcomePending, err)
		}
		switch message.Method {
		case "item/completed":
			var completed itemCompletedNotification
			if err := json.Unmarshal(message.Params, &completed); err != nil {
				return GenerationResult{ThreadID: threadIdentifier, TurnID: turnIdentifier}, fmt.Errorf("%w: decode Luna item completion: %v", ErrGenerationOutcomePending, err)
			}
			if completed.ThreadID == threadIdentifier && completed.TurnID == turnIdentifier && completed.Item.Type == "agentMessage" {
				finalAssistantMessage = completed.Item.Text
			}
		case "thread/tokenUsage/updated":
			var updated threadTokenUsageUpdatedNotification
			if err := json.Unmarshal(message.Params, &updated); err != nil {
				return GenerationResult{ThreadID: threadIdentifier, TurnID: turnIdentifier}, fmt.Errorf("%w: decode Luna token usage: %v", ErrGenerationOutcomePending, err)
			}
			if updated.ThreadID == threadIdentifier && updated.TurnID == turnIdentifier {
				usage := updated.TokenUsage.Last
				finalTokenUsage = &TokenUsage{InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens, OutputTokens: usage.OutputTokens, ReasoningOutputTokens: usage.ReasoningOutputTokens, TotalTokens: usage.TotalTokens}
			}
		case "turn/completed":
			var completed turnCompletedNotification
			if err := json.Unmarshal(message.Params, &completed); err != nil {
				return GenerationResult{ThreadID: threadIdentifier, TurnID: turnIdentifier}, fmt.Errorf("%w: decode Luna turn completion: %v", ErrGenerationOutcomePending, err)
			}
			if completed.ThreadID != threadIdentifier || completed.Turn.ID != turnIdentifier {
				continue
			}
			if completed.Turn.Status != "completed" {
				var generationError error
				if completed.Turn.Error == nil || completed.Turn.Error.Message == "" {
					generationError = fmt.Errorf("Luna turn ended with status %q", completed.Turn.Status)
				} else {
					generationError = fmt.Errorf("Luna turn failed: %s", completed.Turn.Error.Message)
				}
				if deleteErr := client.deleteThreadLocked(ctx, threadIdentifier); deleteErr != nil {
					generationError = errors.Join(generationError, fmt.Errorf("delete failed Luna thread: %w", deleteErr))
				}
				return GenerationResult{ThreadID: threadIdentifier, TurnID: turnIdentifier}, generationError
			}
			result := GenerationResult{ThreadID: threadIdentifier, TurnID: turnIdentifier, RawStructuredOutputJSON: []byte(finalAssistantMessage), TokenUsage: finalTokenUsage}
			if request.ResponseReceived != nil {
				if err := request.ResponseReceived(result); err != nil {
					return result, fmt.Errorf("%w: retain Luna response: %v", ErrGenerationOutcomePending, err)
				}
			}
			if err := client.deleteThreadLocked(ctx, threadIdentifier); err != nil {
				return result, fmt.Errorf("delete retained Luna thread: %w", err)
			}
			if !json.Valid(result.RawStructuredOutputJSON) {
				return GenerationResult{}, errors.New("Luna completed without valid structured output JSON")
			}
			return result, nil
		default:
			if len(message.ID) != 0 {
				return GenerationResult{ThreadID: threadIdentifier, TurnID: turnIdentifier}, fmt.Errorf("%w: Codex app-server requested unsupported method %q", ErrGenerationOutcomePending, message.Method)
			}
		}
	}
}

func (client *Client) readCompletedGenerationLocked(ctx context.Context, threadIdentifier, retainedTurnIdentifier string) (GenerationResult, bool, error) {
	var response threadReadResponse
	if err := client.callLocked(ctx, "thread/read", threadReadParameters{ThreadID: threadIdentifier, IncludeTurns: true}, &response); err != nil {
		return GenerationResult{}, false, err
	}
	if response.Thread.ID != threadIdentifier {
		return GenerationResult{}, false, errors.New("Codex app-server returned a different Luna thread")
	}
	if len(response.Thread.Turns) == 0 {
		return GenerationResult{}, false, nil
	}
	if len(response.Thread.Turns) != 1 {
		return GenerationResult{}, false, errors.New("retained Luna thread contains more than one turn")
	}
	turn := response.Thread.Turns[0]
	if retainedTurnIdentifier != "" && turn.ID != retainedTurnIdentifier {
		return GenerationResult{}, false, errors.New("retained Luna turn identifier changed")
	}
	if turn.Status == "inProgress" {
		return GenerationResult{ThreadID: threadIdentifier, TurnID: turn.ID}, false, errors.New("retained Luna turn is still in progress")
	}
	if turn.Status != "completed" {
		return GenerationResult{ThreadID: threadIdentifier, TurnID: turn.ID}, false, fmt.Errorf("%w: retained turn has status %q", ErrGenerationDidNotComplete, turn.Status)
	}
	var finalAssistantMessage string
	for _, item := range turn.Items {
		if item.Type == "agentMessage" {
			finalAssistantMessage = item.Text
		}
	}
	if strings.TrimSpace(finalAssistantMessage) == "" {
		return GenerationResult{ThreadID: threadIdentifier, TurnID: turn.ID}, false, fmt.Errorf("%w: retained turn has no assistant response", ErrGenerationDidNotComplete)
	}
	return GenerationResult{ThreadID: threadIdentifier, TurnID: turn.ID, RawStructuredOutputJSON: []byte(finalAssistantMessage)}, true, nil
}

func (client *Client) deleteThreadLocked(ctx context.Context, threadIdentifier string) error {
	var response struct{}
	return client.callLocked(ctx, "thread/delete", threadDeleteParameters{ThreadID: threadIdentifier}, &response)
}

func (client *Client) Close() error {
	client.markTerminal()
	<-client.operationSlot
	defer func() { client.operationSlot <- struct{}{} }()
	return client.terminateAndDrain()
}

func (client *Client) runOperation(ctx context.Context, operation func() error) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-client.operationSlot:
	}
	defer func() { client.operationSlot <- struct{}{} }()
	if client.isTerminal() {
		return ErrClientTerminal
	}
	err := operation()
	if cause := context.Cause(ctx); cause != nil {
		if drainError := client.terminateAndDrain(); drainError != nil {
			return errors.Join(cause, err, drainError)
		}
		return errors.Join(cause, err)
	}
	return err
}

func (client *Client) isTerminal() bool {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	return client.terminal
}

func (client *Client) markTerminal() {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	if client.terminal {
		return
	}
	client.terminal = true
	if !client.stdinClosed && client.stdin != nil {
		client.stdinClosed = true
		_ = client.stdin.Close()
	}
	if client.processCancel != nil {
		client.processCancel()
	}
}

func (client *Client) terminateAndDrain() error {
	client.markTerminal()
	var transcriptError error
	for value := range client.messages {
		if err := client.recordProtocolRead(value); err != nil && transcriptError == nil {
			transcriptError = err
		}
	}
	client.waitOnce.Do(func() {
		if client.command != nil {
			_ = client.command.Wait()
		}
	})
	return transcriptError
}

func (client *Client) callLocked(ctx context.Context, method string, parameters any, destination any) error {
	client.nextRequestID++
	requestID := client.nextRequestID
	if err := client.sendLocked(protocolRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  method,
		Params:  parameters,
	}); err != nil {
		return err
	}
	for {
		message, err := client.readLocked(ctx)
		if err != nil {
			return err
		}
		if string(message.ID) != fmt.Sprintf("%d", requestID) {
			if len(message.ID) != 0 {
				return fmt.Errorf("Codex app-server returned an unexpected response identifier %s", message.ID)
			}
			client.pendingNotifications = append(client.pendingNotifications, message)
			continue
		}
		if message.Error != nil {
			return fmt.Errorf("Codex app-server %s failed (%d): %s", method, message.Error.Code, message.Error.Message)
		}
		if err := json.Unmarshal(message.Result, destination); err != nil {
			return fmt.Errorf("decode Codex app-server %s response: %w", method, err)
		}
		return nil
	}
}

func (client *Client) sendLocked(message any) error {
	if client.isTerminal() {
		return ErrClientTerminal
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode Codex app-server request: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := client.writePrivateTranscript("request", encoded); err != nil {
		return err
	}
	if _, err := client.stdin.Write(encoded); err != nil {
		return fmt.Errorf("write Codex app-server request: %w", err)
	}
	return nil
}

func (client *Client) receiveLocked(ctx context.Context) (protocolMessage, error) {
	if len(client.pendingNotifications) != 0 {
		message := client.pendingNotifications[0]
		client.pendingNotifications = client.pendingNotifications[1:]
		return message, nil
	}
	return client.readLocked(ctx)
}

func (client *Client) readLocked(ctx context.Context) (protocolMessage, error) {
	select {
	case <-ctx.Done():
		return protocolMessage{}, context.Cause(ctx)
	case value, open := <-client.messages:
		if !open {
			return protocolMessage{}, errors.New("Codex app-server stopped")
		}
		if err := client.recordProtocolRead(value); err != nil {
			return protocolMessage{}, err
		}
		return value.message, value.err
	}
}

func (client *Client) recordProtocolRead(value protocolRead) error {
	if len(value.encoded) != 0 {
		if err := client.writePrivateTranscript("response", value.encoded); err != nil {
			return err
		}
	}
	if value.err != nil && client.stderrTail != nil {
		if err := client.writePrivateTranscript("stderr-tail", client.stderrTail.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

func readProtocolMessages(reader io.Reader, messages chan<- protocolRead) {
	defer close(messages)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maximumProtocolMessageBytes)
	for scanner.Scan() {
		encoded := append([]byte(nil), scanner.Bytes()...)
		encoded = append(encoded, '\n')
		var message protocolMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			messages <- protocolRead{encoded: encoded, err: fmt.Errorf("decode Codex app-server message: %w", err)}
			return
		}
		messages <- protocolRead{encoded: encoded, message: message}
	}
	if err := scanner.Err(); err != nil {
		messages <- protocolRead{err: fmt.Errorf("read Codex app-server response: %w", err)}
		return
	}
	messages <- protocolRead{err: errors.New("Codex app-server stopped")}
}

func (client *Client) writePrivateTranscript(direction string, encoded []byte) error {
	if client.configuration.PrivateWireTranscript == nil || len(encoded) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(client.configuration.PrivateWireTranscript, "%s\n", direction); err != nil {
		return fmt.Errorf("retain private Luna wire transcript direction: %w", err)
	}
	if _, err := client.configuration.PrivateWireTranscript.Write(encoded); err != nil {
		return fmt.Errorf("retain private Luna wire transcript message: %w", err)
	}
	return nil
}

type protocolRead struct {
	encoded []byte
	message protocolMessage
	err     error
}

type boundedTail struct {
	mu           sync.Mutex
	maximumBytes int
	bytes        []byte
}

func (tail *boundedTail) Write(value []byte) (int, error) {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	if len(value) >= tail.maximumBytes {
		tail.bytes = append(tail.bytes[:0], value[len(value)-tail.maximumBytes:]...)
		return len(value), nil
	}
	overflow := len(tail.bytes) + len(value) - tail.maximumBytes
	if overflow > 0 {
		copy(tail.bytes, tail.bytes[overflow:])
		tail.bytes = tail.bytes[:len(tail.bytes)-overflow]
	}
	tail.bytes = append(tail.bytes, value...)
	return len(value), nil
}

func (tail *boundedTail) Bytes() []byte {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	return append([]byte(nil), tail.bytes...)
}

type protocolRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type protocolNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

type protocolMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *protocolError  `json:"error"`
}

type protocolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeParameters struct {
	ClientInfo clientInformation `json:"clientInfo"`
}

type clientInformation struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type initializeResponse struct {
	UserAgent string `json:"userAgent"`
}

type getAccountParameters struct {
	RefreshToken bool `json:"refreshToken,omitempty"`
}

type getAccountResponse struct {
	Account *struct {
		Type string `json:"type"`
	} `json:"account"`
}

type loginAccountParameters struct {
	Type                      string `json:"type"`
	ApplicationBrand          string `json:"appBrand"`
	CodexStreamlinedLogin     bool   `json:"codexStreamlinedLogin"`
	UseHostedLoginSuccessPage bool   `json:"useHostedLoginSuccessPage"`
}

type loginAccountResponse struct {
	Type              string `json:"type"`
	LoginID           string `json:"loginId"`
	AuthenticationURL string `json:"authUrl"`
}

type accountLoginCompletedNotification struct {
	LoginID string `json:"loginId"`
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

type threadStartParameters struct {
	ApprovalPolicy   string `json:"approvalPolicy"`
	BaseInstructions string `json:"baseInstructions"`
	WorkingDirectory string `json:"cwd"`
	Ephemeral        bool   `json:"ephemeral"`
	Model            string `json:"model"`
	Sandbox          string `json:"sandbox"`
}

type threadStartResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type threadReadParameters struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns"`
}

type threadReadResponse struct {
	Thread struct {
		ID    string `json:"id"`
		Turns []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Items  []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"items"`
		} `json:"turns"`
	} `json:"thread"`
}

type threadDeleteParameters struct {
	ThreadID string `json:"threadId"`
}

type turnInput struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	URL    string `json:"url,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type turnStartParameters struct {
	ThreadID       string                `json:"threadId"`
	Input          []turnInput           `json:"input"`
	ApprovalPolicy string                `json:"approvalPolicy"`
	Model          string                `json:"model"`
	OutputSchema   json.RawMessage       `json:"outputSchema"`
	SandboxPolicy  readOnlySandboxPolicy `json:"sandboxPolicy"`
}

type readOnlySandboxPolicy struct {
	Type          string `json:"type"`
	NetworkAccess bool   `json:"networkAccess"`
}

type turnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type itemCompletedNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Item     struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
}

type turnCompletedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"turn"`
}

type threadTokenUsageUpdatedNotification struct {
	ThreadID   string `json:"threadId"`
	TurnID     string `json:"turnId"`
	TokenUsage struct {
		Last struct {
			TotalTokens           int64 `json:"totalTokens"`
			InputTokens           int64 `json:"inputTokens"`
			CachedInputTokens     int64 `json:"cachedInputTokens"`
			OutputTokens          int64 `json:"outputTokens"`
			ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
		} `json:"last"`
	} `json:"tokenUsage"`
}
