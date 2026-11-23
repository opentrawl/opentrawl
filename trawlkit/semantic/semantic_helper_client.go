package semantic

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	semanticpb "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/semantic"
	"github.com/opentrawl/opentrawl/trawlkit/prototransport"
)

const (
	semanticHelperBinaryName       = "trawl-embed"
	semanticHelperSocketFilename   = "semantic-embedder.sock"
	semanticHelperStartLockName    = "semantic-embedder.start.lock"
	defaultHelperIdleUnloadSeconds = 90
	defaultHelperIdleExitSeconds   = 600
)

// ResolveSemanticHelperSocketPath returns the Unix socket path used by the
// local native MLX embedding helper for stateRoot.
func ResolveSemanticHelperSocketPath(stateRoot string) string {
	return filepath.Join(stateRoot, semanticHelperSocketFilename)
}

// LocateSemanticHelperBinary finds the native Apple Silicon trawl-embed binary
// via OPENTRAWL_EMBEDDER_BINARY or adjacent to the running trawl executable.
func LocateSemanticHelperBinary() (string, bool) {
	if envPath := strings.TrimSpace(os.Getenv("OPENTRAWL_EMBEDDER_BINARY")); envPath != "" {
		if isExecutableFile(envPath) {
			return envPath, true
		}
		return "", false
	}
	exePath, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, evalErr := filepath.EvalSymlinks(exePath); evalErr == nil {
		exePath = resolved
	}
	exeDir := filepath.Dir(exePath)
	candidates := []string{
		filepath.Join(exeDir, semanticHelperBinaryName),
		filepath.Join(exeDir, "..", "Helpers", semanticHelperBinaryName),
		filepath.Join(exeDir, "Helpers", semanticHelperBinaryName),
	}
	for _, candidate := range candidates {
		if isExecutableFile(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// SemanticHelperClient speaks length-delimited binary protobuf over the local
// Unix domain socket to the idle-unloading native Swift/MLX helper.
type SemanticHelperClient struct {
	stateRoot  string
	socketPath string
	helperPath string
}

// NewSemanticHelperClient creates a client targeting stateRoot's local helper.
func NewSemanticHelperClient(stateRoot string) *SemanticHelperClient {
	helperPath, _ := LocateSemanticHelperBinary()
	return &SemanticHelperClient{
		stateRoot:  stateRoot,
		socketPath: ResolveSemanticHelperSocketPath(stateRoot),
		helperPath: helperPath,
	}
}

// HelperBinaryPath returns the located helper binary path, or empty if absent.
func (c *SemanticHelperClient) HelperBinaryPath() string {
	return c.helperPath
}

// CheckRunningHealth probes an already-running helper without starting one.
func (c *SemanticHelperClient) CheckRunningHealth(ctx context.Context) (*semanticpb.SemanticEmbedderHealthResponse, bool, error) {
	resp, err := c.sendRequestWithoutStart(ctx, &semanticpb.SemanticEmbedderRequest{
		RequestPayload: &semanticpb.SemanticEmbedderRequest_HealthCheck{
			HealthCheck: &semanticpb.SemanticEmbedderHealthCheckRequest{},
		},
	}, 2*time.Second)
	if err != nil {
		return nil, false, nil
	}
	if fail := resp.GetFailure(); fail != nil {
		return nil, true, errors.New(fail.GetFailureMessage())
	}
	return resp.GetHealth(), true, nil
}

// ShutdownIfRunning asks a running helper to unload weights and exit cleanly.
func (c *SemanticHelperClient) ShutdownIfRunning(ctx context.Context) error {
	_, err := c.sendRequestWithoutStart(ctx, &semanticpb.SemanticEmbedderRequest{
		RequestPayload: &semanticpb.SemanticEmbedderRequest_Shutdown{
			Shutdown: &semanticpb.SemanticEmbedderShutdownRequest{},
		},
	}, 3*time.Second)
	if err != nil {
		return nil
	}
	return nil
}

// EmbedTexts starts the local helper on demand if needed and returns one
// quantized int8 vector per input text.
func (c *SemanticHelperClient) EmbedTexts(
	ctx context.Context,
	modelDir string,
	modelIdentifier string,
	texts []string,
	dimensions uint32,
	maxTokens uint32,
	isQuery bool,
) ([][]byte, uint64, error) {
	if len(texts) == 0 {
		return nil, 0, nil
	}
	if err := c.ensureHelperRunning(ctx); err != nil {
		return nil, 0, err
	}
	timeout := 30 * time.Second
	if len(texts) > 32 {
		timeout = 5 * time.Minute
	}
	resp, err := c.sendRequestWithoutStart(ctx, &semanticpb.SemanticEmbedderRequest{
		RequestPayload: &semanticpb.SemanticEmbedderRequest_EmbedTexts{
			EmbedTexts: &semanticpb.SemanticEmbedderEmbedTextsRequest{
				LocalModelDirectoryPath:    modelDir,
				ModelIdentifier:            modelIdentifier,
				InputTexts:                 texts,
				OutputEmbeddingDimensions:  dimensions,
				MaximumInputTokenCount:     maxTokens,
				InputRepresentsSearchQuery: isQuery,
			},
		},
	}, timeout)
	if err != nil {
		return nil, 0, err
	}
	if fail := resp.GetFailure(); fail != nil {
		return nil, 0, errors.New(fail.GetFailureMessage())
	}
	emb := resp.GetEmbeddings()
	if emb == nil {
		return nil, 0, errors.New("semantic embedder returned an empty embeddings response")
	}
	if len(emb.GetQuantizedInt8Vectors()) != len(texts) {
		return nil, 0, fmt.Errorf(
			"semantic embedder returned %d vectors for %d input texts",
			len(emb.GetQuantizedInt8Vectors()),
			len(texts),
		)
	}
	return emb.GetQuantizedInt8Vectors(), emb.GetElapsedMilliseconds(), nil
}

func (c *SemanticHelperClient) ensureHelperRunning(ctx context.Context) error {
	if _, running, _ := c.CheckRunningHealth(ctx); running {
		return nil
	}
	if c.helperPath == "" {
		return errors.New("native semantic helper binary (trawl-embed) was not found")
	}
	if err := os.MkdirAll(c.stateRoot, 0o700); err != nil {
		return fmt.Errorf("create state directory for semantic helper: %w", err)
	}

	lockPath := filepath.Join(c.stateRoot, semanticHelperStartLockName)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- fixed lock path inside stateRoot.
	if err != nil {
		return fmt.Errorf("open semantic helper startup lock: %w", err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock semantic helper startup: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	if _, running, _ := c.CheckRunningHealth(ctx); running {
		return nil
	}

	cmd := exec.Command( // #nosec G204 -- helper binary path is located from app bundle or explicit env.
		c.helperPath,
		"--socket", c.socketPath,
		"--idle-unload-seconds", fmt.Sprintf("%d", defaultHelperIdleUnloadSeconds),
		"--idle-exit-seconds", fmt.Sprintf("%d", defaultHelperIdleExitSeconds),
	)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start native semantic helper: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, running, _ := c.CheckRunningHealth(ctx); running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return errors.New("timed out waiting for native semantic helper socket")
}

func (c *SemanticHelperClient) sendRequestWithoutStart(
	ctx context.Context,
	req *semanticpb.SemanticEmbedderRequest,
	timeout time.Duration,
) (*semanticpb.SemanticEmbedderResponse, error) {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	if err := prototransport.WriteDelimited(conn, req); err != nil {
		return nil, fmt.Errorf("send semantic embedder request: %w", err)
	}
	var resp semanticpb.SemanticEmbedderResponse
	if err := prototransport.ReadDelimited(bufio.NewReader(conn), &resp); err != nil {
		return nil, fmt.Errorf("read semantic embedder response: %w", err)
	}
	return &resp, nil
}
