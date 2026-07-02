package gog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBinary       = "gog"
	DefaultPageSize     = 100
	DefaultArchiveQuery = "-in:chats"
)

type Client struct {
	Binary string
}

type MessagePage struct {
	Messages      []Message
	NextPageToken string
}

type Message struct {
	ID          string
	ThreadID    string
	Time        time.Time
	FromName    string
	FromAddress string
	Subject     string
	Labels      []string
	Body        string
}

type SearchRequest struct {
	Query     string
	Max       int
	PageToken string
}

type AuthStatus struct {
	FoundAccount bool
	Authorized   bool
	Expires      *time.Time
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

type searchResponse struct {
	Messages      []searchMessage `json:"messages"`
	NextPageToken string          `json:"nextPageToken"`
}

type searchMessage struct {
	ID       string   `json:"id"`
	ThreadID string   `json:"threadId"`
	Date     string   `json:"date"`
	From     string   `json:"from"`
	Subject  string   `json:"subject"`
	Labels   []string `json:"labels"`
	Body     string   `json:"body"`
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

func (c Client) SearchMessages(ctx context.Context, req SearchRequest) (MessagePage, error) {
	max := req.Max
	if max <= 0 {
		max = DefaultPageSize
	}
	args := []string{"gmail", "messages", "search", "--json", "--max", strconv.Itoa(max)}
	if token := strings.TrimSpace(req.PageToken); token != "" {
		args = append(args, "--page", token)
	}
	// The query goes last, behind "--": archive queries like "-in:chats"
	// start with a dash and gog would otherwise read them as flags.
	args = append(args, "--include-body", "--", strings.TrimSpace(req.Query))
	data, err := c.run(ctx, args...)
	if err != nil {
		return MessagePage{}, err
	}
	var raw searchResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return MessagePage{}, fmt.Errorf("decode gog message search: %w", err)
	}
	out := MessagePage{NextPageToken: strings.TrimSpace(raw.NextPageToken)}
	for _, item := range raw.Messages {
		msg, err := parseSearchMessage(item)
		if err != nil {
			return MessagePage{}, err
		}
		out.Messages = append(out.Messages, msg)
	}
	return out, nil
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
		expires := parseExpiry(fields[3])
		valid := strings.EqualFold(strings.TrimSpace(fields[4]), "true")
		if valid {
			status.Authorized = true
			if expires != nil {
				status.Expires = expires
			}
			return status, nil
		}
		if status.Expires == nil && expires != nil {
			status.Expires = expires
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

func (c Client) binary() string {
	if strings.TrimSpace(c.Binary) == "" {
		return DefaultBinary
	}
	return c.Binary
}

func parseSearchMessage(raw searchMessage) (Message, error) {
	// A message with an unreadable or absent date keeps a zero time
	// rather than aborting the crawl; the read path renders it dateless.
	when, _ := parseDate(raw.Date)
	name, address := parseAddress(raw.From)
	return Message{
		ID:          strings.TrimSpace(raw.ID),
		ThreadID:    strings.TrimSpace(raw.ThreadID),
		Time:        when,
		FromName:    name,
		FromAddress: address,
		Subject:     strings.TrimSpace(raw.Subject),
		Labels:      append([]string(nil), raw.Labels...),
		Body:        raw.Body,
	}, nil
}

func parseAddress(value string) (string, string) {
	value = strings.TrimSpace(value)
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return "", value
	}
	return strings.TrimSpace(addr.Name), strings.TrimSpace(addr.Address)
}

func parseDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty date")
	}
	if parsed, err := mail.ParseDate(value); err == nil {
		return parsed, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, time.RFC1123Z, time.RFC1123} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	// gog renders message dates in local time, usually "2026-07-02 15:19"
	// but sometimes "17 mar 2026 11:09:45" (lowercase month) for older mail.
	titled := strings.Title(strings.ToLower(value)) //nolint:staticcheck // ASCII month names only
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2 Jan 2006 15:04:05", "2 Jan 2006 15:04"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, nil
		}
		if parsed, err := time.ParseInLocation(layout, titled, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date %q", value)
}

func parseExpiry(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return &parsed
}

func commandError(args []string, err error, stderr string) error {
	name := "gog"
	if len(args) > 0 {
		limit := len(args)
		if limit > 3 {
			limit = 3
		}
		name += " " + strings.Join(args[:limit], " ")
	}
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	if lines := strings.Split(stderr, "\n"); len(lines) > 1 {
		stderr = lines[len(lines)-1]
	}
	return fmt.Errorf("%s failed: %w: %s", name, err, stderr)
}
