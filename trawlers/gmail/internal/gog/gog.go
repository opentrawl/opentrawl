package gog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"os/exec"
	"strconv"
	"strings"
)

const (
	DefaultBinary   = "gog"
	DefaultPageSize = 100
)

type Client struct {
	Binary string
}

type AuthStatus struct {
	FoundAccount bool
	Authorized   bool
	AccountEmail string
}

type ContactPage struct {
	Contacts      []Contact
	NextPageToken string
}

type Contact struct {
	Resource string
	Name     string
	Phone    string
}

type BackupPushRequest struct {
	Repo  string
	Query string
	Max   int
}

type contactsResponse struct {
	Contacts      []Contact `json:"contacts"`
	NextPageToken string    `json:"nextPageToken"`
}

func New(binary string) Client {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		binary = DefaultBinary
	}
	return Client{Binary: binary}
}

func (c Client) Version(ctx context.Context) (string, error) {
	data, err := c.run(ctx, "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (c Client) AuthStatus(ctx context.Context) (AuthStatus, error) {
	data, err := c.run(ctx, "auth", "list", "--check", "--plain")
	if err != nil {
		return AuthStatus{}, err
	}
	status := AuthStatus{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		status.FoundAccount = true
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}
		email := strings.TrimSpace(fields[0])
		if status.AccountEmail == "" {
			status.AccountEmail = email
		}
		valid := strings.EqualFold(strings.TrimSpace(fields[4]), "true")
		if valid {
			status.Authorized = true
			status.AccountEmail = email
			return status, nil
		}
	}
	return status, nil
}

func (c Client) Contacts(ctx context.Context, max int, pageToken string) (ContactPage, error) {
	if max <= 0 {
		max = DefaultPageSize
	}
	args := []string{"contacts", "list", "--json", "--max", strconv.Itoa(max)}
	if token := strings.TrimSpace(pageToken); token != "" {
		args = append(args, "--page", token)
	}
	data, err := c.run(ctx, args...)
	if err != nil {
		return ContactPage{}, err
	}
	var raw contactsResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return ContactPage{}, fmt.Errorf("decode gog contacts: %w", err)
	}
	raw.NextPageToken = strings.TrimSpace(raw.NextPageToken)
	return ContactPage(raw), nil
}

func (c Client) BackupInit(ctx context.Context, repo string) error {
	if strings.TrimSpace(repo) == "" {
		return fmt.Errorf("backup repo path is required")
	}
	_, err := c.run(ctx, "backup", "init", "--no-push", "--repo", repo)
	return err
}

func (c Client) BackupGmailPush(ctx context.Context, req BackupPushRequest) error {
	if strings.TrimSpace(req.Repo) == "" {
		return fmt.Errorf("backup repo path is required")
	}
	args := []string{"backup", "gmail", "push", "--no-push", "--gmail-cache", "--repo", req.Repo}
	if query := strings.TrimSpace(req.Query); query != "" {
		args = append(args, "--query", query)
	}
	if req.Max > 0 {
		args = append(args, "--max", strconv.Itoa(req.Max))
	}
	_, err := c.run(ctx, args...)
	return err
}

func (c Client) BackupCat(ctx context.Context, repo, shard string) ([]byte, error) {
	if strings.TrimSpace(repo) == "" {
		return nil, fmt.Errorf("backup repo path is required")
	}
	if strings.TrimSpace(shard) == "" {
		return nil, fmt.Errorf("backup shard path is required")
	}
	return c.run(ctx, "backup", "cat", "--no-pull", "--repo", repo, shard)
}

func (c Client) LookPath() (string, error) {
	return exec.LookPath(c.binary())
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.binary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, commandError(args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func commandError(args []string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return fmt.Errorf("gog %s: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("gog %s: %w: %s", strings.Join(args, " "), err, stderr)
}

func (c Client) binary() string {
	if strings.TrimSpace(c.Binary) == "" {
		return DefaultBinary
	}
	return c.Binary
}

func ParseAddress(value string) (string, string) {
	value = strings.TrimSpace(value)
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return "", value
	}
	return strings.TrimSpace(addr.Name), strings.TrimSpace(addr.Address)
}
