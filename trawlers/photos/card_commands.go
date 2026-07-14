package photoscrawl

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const preparedCardDirectory = "prepared-cards"

type CardModelConfig struct {
	ProviderIdentity string `toml:"provider_identity"`
	BaseURL          string `toml:"base_url"`
	Model            string `toml:"model"`
	CredentialEnv    string `toml:"credential_env"`
}

func (c CardModelConfig) configured() bool {
	return strings.TrimSpace(c.ProviderIdentity) != "" || strings.TrimSpace(c.BaseURL) != "" ||
		strings.TrimSpace(c.Model) != "" || strings.TrimSpace(c.CredentialEnv) != ""
}

func (c CardModelConfig) validate() error {
	for field, value := range map[string]string{
		"provider_identity": c.ProviderIdentity, "base_url": c.BaseURL, "model": c.Model, "credential_env": c.CredentialEnv,
	} {
		if strings.TrimSpace(value) == "" {
			return configError("card_model."+field, "set every field in [card_model] explicitly", field+" is required")
		}
	}
	if strings.ContainsAny(c.ProviderIdentity, "\r\n\t") {
		return configError("card_model.provider_identity", "set provider_identity to one plain display name", "provider_identity must fit on one line")
	}
	if _, err := model.NormalizeBaseURL(c.BaseURL); err != nil {
		return configError("card_model.base_url", "set the exact model provider base URL", err.Error())
	}
	if !validEnvironmentName(c.CredentialEnv) {
		return configError("card_model.credential_env", "set credential_env to the environment variable supplied by secret management", "credential_env must be an environment variable name")
	}
	return nil
}

func (c CardModelConfig) requireCredential() error {
	if strings.TrimSpace(os.Getenv(strings.TrimSpace(c.CredentialEnv))) == "" {
		return configError("card_model.credential_env", "make the configured credential available, then retry", fmt.Sprintf("credential %s is unavailable", strings.TrimSpace(c.CredentialEnv)))
	}
	return nil
}

type cardRequestResult struct {
	Type           string   `json:"type"`
	Photo          string   `json:"photo"`
	Provider       string   `json:"provider"`
	Endpoint       string   `json:"endpoint"`
	Model          string   `json:"model"`
	CredentialEnv  string   `json:"credential_env"`
	Sends          []string `json:"sends"`
	RequestSHA256  string   `json:"request_sha256"`
	ApprovalSHA256 string   `json:"approval_sha256"`
	CallCap        int      `json:"call_cap"`
	State          string   `json:"state"`
}

type cardCreationResult struct {
	Type  string `json:"type"`
	Photo string `json:"photo"`
	Model string `json:"model"`
	State string `json:"state"`
}

func (c *Crawler) runPrepareCard(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 1 {
		return output.UsageError{Err: errors.New("prepare-card requires one photo ref")}
	}
	if err := c.cfg.CardModel.validate(); err != nil {
		return err
	}
	ref, err := c.resolveCardRef(ctx, req.Paths.Archive, req.Args[0])
	if err != nil {
		return err
	}
	bundle, err := archive.PrepareApprovedCardBundle(ctx, archive.ApprovedCardPrepareOptions{
		ArchivePath: req.Paths.Archive, CacheDir: archivePaths(req).CacheDir,
		AssetIDs: []string{archive.AssetID(ref)}, Model: c.cfg.CardModel.Model,
		ModelURL: c.cfg.CardModel.BaseURL, ProviderIdentity: c.cfg.CardModel.ProviderIdentity,
		CredentialEnv: c.cfg.CardModel.CredentialEnv,
		Purpose:       "canary", CallCap: 1,
	})
	if err != nil {
		return err
	}
	approval, err := storePreparedCard(req.Paths.Archive, bundle)
	if err != nil {
		return err
	}
	review, err := archive.ReviewApprovedCardBundle(bundle)
	if err != nil {
		return err
	}
	result := cardRequestResult{
		Type: "photos.card_request.v1", Photo: review.PhotoRef,
		Provider: review.ProviderIdentity, Endpoint: review.Endpoint,
		Model: review.Model, CredentialEnv: review.CredentialEnv,
		Sends:         []string{"one current photo", "checked Photos evidence"},
		RequestSHA256: review.RequestSHA256, ApprovalSHA256: approval, CallCap: review.CallCap, State: review.State,
	}
	return writeCardRequest(req, result)
}

func writeCardRequest(req *trawlkit.Request, result cardRequestResult) error {
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "card_request", result)
	}
	_, err := fmt.Fprintf(req.Out, "Photos card ready to approve\n\nPhoto           %s\nSends           one current photo and its checked Photos evidence\nProvider        %s\nEndpoint        %s\nModel           %s\nCredential env  %s\nCall limit      1\nStatus          Nothing has been sent\n\nApproval        %s\n\nCreate this card:\n  trawl photos create-card %s\n", result.Photo, result.Provider, result.Endpoint, result.Model, result.CredentialEnv, result.ApprovalSHA256, result.ApprovalSHA256)
	return err
}

func (c *Crawler) runCreateCard(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 1 {
		return output.UsageError{Err: errors.New("create-card requires one approval digest")}
	}
	if err := c.cfg.CardModel.validate(); err != nil {
		return err
	}
	approval := strings.TrimSpace(req.Args[0])
	if !validApprovalDigest(approval) {
		return output.UsageError{Err: errors.New("create-card requires a bare lowercase SHA-256 approval digest")}
	}
	bundle, err := readPreparedCard(req.Paths.Archive, approval, c.cfg.CardModel.CredentialEnv)
	if err != nil {
		return err
	}
	if _, err := archive.ReviewApprovedCardBundle(bundle); err != nil {
		return err
	}
	completed, found, err := archive.CompletedApprovedCardBundle(ctx, req.Paths.Archive, bundle)
	if err != nil {
		return err
	}
	if found {
		return writeApprovedCardResult(req, completed)
	}
	if err := archive.ValidateApprovedCardBundleFreshness(ctx, bundle, archive.ApprovedCardPrepareOptions{
		ArchivePath: req.Paths.Archive, CacheDir: archivePaths(req).CacheDir,
		Model: c.cfg.CardModel.Model, ModelURL: c.cfg.CardModel.BaseURL,
		ProviderIdentity: c.cfg.CardModel.ProviderIdentity,
		CredentialEnv:    c.cfg.CardModel.CredentialEnv,
	}); err != nil {
		return err
	}
	if err := c.cfg.CardModel.requireCredential(); err != nil {
		return err
	}
	client, err := model.New(model.Config{BaseURL: c.cfg.CardModel.BaseURL, Model: c.cfg.CardModel.Model, BearerKeyEnv: c.cfg.CardModel.CredentialEnv})
	if err != nil {
		return err
	}
	db, err := archive.OpenApprovedCardArchive(ctx, req.Paths.Archive)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	sent, err := archive.SendApprovedCardBundle(ctx, db, bundle, approval, c.cfg.CardModel.CredentialEnv, time.Now().UTC(), client)
	if err != nil {
		return err
	}
	return writeApprovedCardResult(req, sent)
}

func writeApprovedCardResult(req *trawlkit.Request, sent archive.ApprovedCardSendResult) error {
	if len(sent.Items) != 1 {
		return errors.New("card creation did not return one photo")
	}
	result := cardCreationResult{
		Type: "photos.card_creation.v1", Photo: archive.AssetRef(sent.Items[0].AssetID),
		Model: sent.Items[0].Model, State: sent.Items[0].State,
	}
	return writeCardCreation(req, result)
}

func writeCardCreation(req *trawlkit.Request, result cardCreationResult) error {
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "card_creation", result)
	}
	title := "Card created"
	note := ""
	if result.State == "already_created" {
		title = "Card already exists"
		note = "\nNo model request was sent.\n"
	}
	_, err := fmt.Fprintf(req.Out, "%s\n\nPhoto  %s\n%s\nOpen it with:\n  trawl photos open %s\n", title, result.Photo, note, result.Photo)
	return err
}

func validApprovalDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func (c *Crawler) resolveCardRef(ctx context.Context, archivePath, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") || strings.Contains(ref, "/") {
		return ref, nil
	}
	if !trawlkit.ValidShortRef(ref) {
		return "", commandError{Code: "invalid_ref", Message: "ref is not a Photos asset ref", Remedy: "use a Photos asset ref or a short ref from search"}
	}
	db, err := store.OpenReadOnly(ctx, archivePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	refs, err := (&trawlkit.Request{Store: db}).ResolveShortRef(ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found", Remedy: "rerun search or use the full ref"}
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandError{Code: "ambiguous_short_ref", Message: "short ref matches more than one asset", Remedy: "rerun search or use the full ref"}
	}
	if err != nil {
		return "", err
	}
	if len(refs) != 1 {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found", Remedy: "rerun search or use the full ref"}
	}
	return refs[0], nil
}

func preparedCardPath(archivePath, approval string) string {
	return filepath.Join(filepath.Dir(archivePath), preparedCardDirectory, approval+".pb")
}

func storePreparedCard(archivePath string, bundle []byte) (string, error) {
	approval, err := archive.ApprovedCardApprovalDigest(bundle)
	if err != nil {
		return "", err
	}
	path := preparedCardPath(archivePath, approval)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create prepared card store: %w", pathlessFileError(err))
	}
	if err := writePreparedCardOnce(path, bundle); err != nil {
		return "", fmt.Errorf("store prepared card request: %w", pathlessFileError(err))
	}
	return approval, nil
}

func writePreparedCardOnce(path string, bundle []byte) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".prepared-card-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(temporary, bytes.NewReader(bundle)); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		stored, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(stored, bundle) {
			return errors.New("prepared card approval already names different bytes")
		}
		return nil
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

func readPreparedCard(archivePath, approval, credentialEnv string) ([]byte, error) {
	bundle, err := os.ReadFile(preparedCardPath(archivePath, approval))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("prepared card approval was not found; run prepare-card again")
		}
		return nil, fmt.Errorf("read prepared card request: %w", pathlessFileError(err))
	}
	if err := archive.ValidateApprovedCardSend(bundle, approval, credentialEnv); err != nil {
		return nil, err
	}
	return bundle, nil
}

func pathlessFileError(err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return pathError.Err
	}
	var linkError *os.LinkError
	if errors.As(err, &linkError) {
		return linkError.Err
	}
	return err
}
