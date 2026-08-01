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

// StructuredOutputSchema is a validated JSON Schema at the external protocol
// boundary. The Photos DAG must construct it from the PhotoCard Protobuf
// descriptor rather than maintain a second handwritten card contract.
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
	Instructions   string
	Image          []byte
	ImageMediaType ImageMediaType
	OutputSchema   StructuredOutputSchema
}

type GenerationResult struct {
	ThreadID                string
	TurnID                  string
	RawStructuredOutputJSON []byte
}

// Client owns one headless app-server process. It is an internal transport,
// not a second macOS app and not a Photos-library permission identity.
type Client struct {
	command    *exec.Cmd
	stdin      io.WriteCloser
	messages   <-chan protocolRead
	stderrTail *boundedTail

	mu                   sync.Mutex
	nextRequestID        uint64
	closed               bool
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

	command := exec.CommandContext(
		ctx,
		configuration.CodexExecutablePath,
		"app-server",
		"--stdio",
		"--config",
		`shell_environment_policy.inherit="none"`,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex app-server input: %w", err)
	}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex app-server output: %w", err)
	}
	stderrTail := &boundedTail{maximumBytes: 64 << 10}
	command.Stderr = stderrTail
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}

	protocolMessages := make(chan protocolRead, 1)
	go readProtocolMessages(stdoutPipe, protocolMessages)
	client := &Client{
		command:       command,
		stdin:         stdin,
		messages:      protocolMessages,
		stderrTail:    stderrTail,
		configuration: configuration,
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	var initialized initializeResponse
	if err := client.callLocked(ctx, "initialize", initializeParameters{
		ClientInfo: clientInformation{
			Name:    "opentrawl",
			Title:   "OpenTrawl",
			Version: configuration.ClientVersion,
		},
	}, &initialized); err != nil {
		_ = client.closeLocked()
		return nil, err
	}
	if err := client.sendLocked(protocolNotification{JSONRPC: "2.0", Method: "initialized"}); err != nil {
		_ = client.closeLocked()
		return nil, err
	}
	return client, nil
}

func (client *Client) Account(ctx context.Context) (Account, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return Account{}, err
	}
	var response getAccountResponse
	if err := client.callLocked(ctx, "account/read", getAccountParameters{}, &response); err != nil {
		return Account{}, err
	}
	if response.Account == nil {
		return Account{Kind: AccountNone}, nil
	}
	switch response.Account.Type {
	case string(AccountChatGPT):
		return Account{Kind: AccountChatGPT}, nil
	case string(AccountAPIKey):
		return Account{Kind: AccountAPIKey}, nil
	default:
		return Account{}, fmt.Errorf("unsupported Codex account type %q", response.Account.Type)
	}
}

func (client *Client) BeginChatGPTSignIn(ctx context.Context) (ChatGPTSignIn, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return ChatGPTSignIn{}, err
	}
	var response loginAccountResponse
	if err := client.callLocked(ctx, "account/login/start", loginAccountParameters{
		Type:                      string(AccountChatGPT),
		ApplicationBrand:          "chatgpt",
		CodexStreamlinedLogin:     false,
		UseHostedLoginSuccessPage: true,
	}, &response); err != nil {
		return ChatGPTSignIn{}, err
	}
	if response.Type != string(AccountChatGPT) || response.LoginID == "" || response.AuthenticationURL == "" {
		return ChatGPTSignIn{}, errors.New("Codex app-server returned an incomplete ChatGPT sign-in")
	}
	authenticationURL, err := url.Parse(response.AuthenticationURL)
	if err != nil {
		return ChatGPTSignIn{}, fmt.Errorf("parse ChatGPT sign-in URL: %w", err)
	}
	if authenticationURL.Scheme != "https" || authenticationURL.Host == "" {
		return ChatGPTSignIn{}, errors.New("Codex app-server returned an unsafe ChatGPT sign-in URL")
	}
	return ChatGPTSignIn{LoginID: response.LoginID, URL: authenticationURL}, nil
}

func (client *Client) WaitForChatGPTSignIn(ctx context.Context, loginID string) error {
	client.mu.Lock()
	defer client.mu.Unlock()
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

	client.mu.Lock()
	defer client.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return GenerationResult{}, err
	}
	var accountResponse getAccountResponse
	if err := client.callLocked(ctx, "account/read", getAccountParameters{RefreshToken: true}, &accountResponse); err != nil {
		return GenerationResult{}, err
	}
	if accountResponse.Account == nil || accountResponse.Account.Type != string(AccountChatGPT) {
		return GenerationResult{}, ErrChatGPTSignInRequired
	}

	var threadResponse threadStartResponse
	if err := client.callLocked(ctx, "thread/start", threadStartParameters{
		ApprovalPolicy:   "never",
		BaseInstructions: "Return only the requested structured result. Do not inspect files, run commands, use tools, browse, or ask questions.",
		WorkingDirectory: client.configuration.EmptyWorkingDirectory,
		Ephemeral:        true,
		Model:            ModelGPT56Luna,
		Sandbox:          "read-only",
	}, &threadResponse); err != nil {
		return GenerationResult{}, err
	}
	if threadResponse.Thread.ID == "" {
		return GenerationResult{}, errors.New("Codex app-server started a Luna thread without an identifier")
	}

	imageDataURL := "data:" + string(request.ImageMediaType) + ";base64," + base64.StdEncoding.EncodeToString(request.Image)
	var turnResponse turnStartResponse
	if err := client.callLocked(ctx, "turn/start", turnStartParameters{
		ThreadID: threadResponse.Thread.ID,
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
		return GenerationResult{}, err
	}
	if turnResponse.Turn.ID == "" {
		return GenerationResult{}, errors.New("Codex app-server started a Luna turn without an identifier")
	}

	var finalAssistantMessage string
	for {
		message, err := client.receiveLocked(ctx)
		if err != nil {
			return GenerationResult{}, err
		}
		switch message.Method {
		case "item/completed":
			var completed itemCompletedNotification
			if err := json.Unmarshal(message.Params, &completed); err != nil {
				return GenerationResult{}, fmt.Errorf("decode Luna item completion: %w", err)
			}
			if completed.ThreadID == threadResponse.Thread.ID && completed.TurnID == turnResponse.Turn.ID && completed.Item.Type == "agentMessage" {
				finalAssistantMessage = completed.Item.Text
			}
		case "turn/completed":
			var completed turnCompletedNotification
			if err := json.Unmarshal(message.Params, &completed); err != nil {
				return GenerationResult{}, fmt.Errorf("decode Luna turn completion: %w", err)
			}
			if completed.ThreadID != threadResponse.Thread.ID || completed.Turn.ID != turnResponse.Turn.ID {
				continue
			}
			if completed.Turn.Status != "completed" {
				if completed.Turn.Error == nil || completed.Turn.Error.Message == "" {
					return GenerationResult{}, fmt.Errorf("Luna turn ended with status %q", completed.Turn.Status)
				}
				return GenerationResult{}, fmt.Errorf("Luna turn failed: %s", completed.Turn.Error.Message)
			}
			if !json.Valid([]byte(finalAssistantMessage)) {
				return GenerationResult{}, errors.New("Luna completed without valid structured output JSON")
			}
			return GenerationResult{
				ThreadID:                threadResponse.Thread.ID,
				TurnID:                  turnResponse.Turn.ID,
				RawStructuredOutputJSON: []byte(finalAssistantMessage),
			}, nil
		default:
			if len(message.ID) != 0 {
				return GenerationResult{}, fmt.Errorf("Codex app-server requested unsupported method %q", message.Method)
			}
		}
	}
}

func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.closeLocked()
}

func (client *Client) closeLocked() error {
	if client.closed {
		return nil
	}
	client.closed = true
	if client.stdin != nil {
		_ = client.stdin.Close()
	}
	if client.command == nil {
		return nil
	}
	if err := client.command.Wait(); err != nil {
		return fmt.Errorf("stop Codex app-server: %w", err)
	}
	return nil
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
	if client.closed {
		return errors.New("Codex app-server client is closed")
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
		if len(value.encoded) != 0 {
			if err := client.writePrivateTranscript("response", value.encoded); err != nil {
				return protocolMessage{}, err
			}
		}
		if value.err != nil && client.stderrTail != nil {
			if err := client.writePrivateTranscript("stderr-tail", client.stderrTail.Bytes()); err != nil {
				return protocolMessage{}, err
			}
		}
		return value.message, value.err
	}
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
