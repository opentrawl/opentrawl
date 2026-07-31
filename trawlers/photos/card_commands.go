package photos

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
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
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
			return configError("card_model."+field, field+" is required")
		}
	}
	if strings.ContainsAny(c.ProviderIdentity, "\r\n\t") {
		return configError("card_model.provider_identity", "provider_identity must fit on one line")
	}
	if _, err := model.NormalizeBaseURL(c.BaseURL); err != nil {
		return configError("card_model.base_url", err.Error())
	}
	if !validEnvironmentName(c.CredentialEnv) {
		return configError("card_model.credential_env", "credential_env must be an environment variable name")
	}
	return nil
}

func (c CardModelConfig) requireCredential() error {
	if strings.TrimSpace(os.Getenv(strings.TrimSpace(c.CredentialEnv))) == "" {
		return configError("card_model.credential_env", fmt.Sprintf("credential %s is unavailable", strings.TrimSpace(c.CredentialEnv)))
	}
	return nil
}

func (c *Crawler) runPrepareCard(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 1 {
		return nil, output.UsageError{Err: errors.New("prepare-card requires one photo ref")}
	}
	if err := c.cfg.CardModel.validate(); err != nil {
		return nil, err
	}
	ref, err := c.resolveCardRef(ctx, req.TrawlerArchivePaths.TrawlerArchivePath, req.TrawlerCommandPositionalArguments[0])
	if err != nil {
		return nil, err
	}
	bundle, err := archive.PrepareApprovedCardBundle(ctx, archive.ApprovedCardPrepareOptions{
		ArchivePath: req.TrawlerArchivePaths.TrawlerArchivePath, CacheDir: archivePaths(req).CacheDir,
		AssetIDs: []string{archive.AssetID(ref)}, Model: c.cfg.CardModel.Model,
		ModelURL: c.cfg.CardModel.BaseURL, ProviderIdentity: c.cfg.CardModel.ProviderIdentity,
		CredentialEnv: c.cfg.CardModel.CredentialEnv,
		Purpose:       "canary", CallCap: 1,
	})
	if err != nil {
		return nil, err
	}
	approval, err := storePreparedCard(req.TrawlerArchivePaths.TrawlerArchivePath, bundle)
	if err != nil {
		return nil, err
	}
	review, err := archive.ReviewApprovedCardBundle(bundle)
	if err != nil {
		return nil, err
	}
	return photosDetailCommandResponse("Photos card ready to approve",
		photosDetailCanonicalRecordReferenceField("Photo", review.PhotoRef),
		photosDetailTextField("Provider", review.ProviderIdentity),
		photosDetailTextField("Endpoint", review.Endpoint),
		photosDetailTextField("Model", review.Model),
		photosDetailTextField("Credential environment", review.CredentialEnv),
		photosDetailUnsignedCountField("Call limit", int64(review.CallCap)),
		photosDetailTextField("State", review.State),
		photosDetailTextField("Approval", approval)), nil
}

func (c *Crawler) runCreateCard(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 1 {
		return nil, output.UsageError{Err: errors.New("create-card requires one approval digest")}
	}
	if err := c.cfg.CardModel.validate(); err != nil {
		return nil, err
	}
	approval := strings.TrimSpace(req.TrawlerCommandPositionalArguments[0])
	if !validApprovalDigest(approval) {
		return nil, output.UsageError{Err: errors.New("create-card requires a bare lowercase SHA-256 approval digest")}
	}
	bundle, err := readPreparedCard(req.TrawlerArchivePaths.TrawlerArchivePath, approval, c.cfg.CardModel.CredentialEnv)
	if err != nil {
		return nil, err
	}
	if _, err := archive.ReviewApprovedCardBundle(bundle); err != nil {
		return nil, err
	}
	completed, found, err := archive.CompletedApprovedCardBundle(ctx, req.TrawlerArchivePaths.TrawlerArchivePath, bundle)
	if err != nil {
		return nil, err
	}
	if found {
		return approvedCardCommandResponse(completed)
	}
	if err := archive.ValidateApprovedCardBundleFreshness(ctx, bundle, archive.ApprovedCardPrepareOptions{
		ArchivePath: req.TrawlerArchivePaths.TrawlerArchivePath, CacheDir: archivePaths(req).CacheDir,
		Model: c.cfg.CardModel.Model, ModelURL: c.cfg.CardModel.BaseURL,
		ProviderIdentity: c.cfg.CardModel.ProviderIdentity,
		CredentialEnv:    c.cfg.CardModel.CredentialEnv,
	}); err != nil {
		return nil, err
	}
	if err := c.cfg.CardModel.requireCredential(); err != nil {
		return nil, err
	}
	client, err := model.New(model.Config{BaseURL: c.cfg.CardModel.BaseURL, Model: c.cfg.CardModel.Model, BearerKeyEnv: c.cfg.CardModel.CredentialEnv})
	if err != nil {
		return nil, err
	}
	db, err := archive.OpenApprovedCardArchive(ctx, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	sent, err := archive.SendApprovedCardBundle(ctx, db, bundle, approval, c.cfg.CardModel.CredentialEnv, time.Now().UTC(), client)
	if err != nil {
		return nil, err
	}
	return approvedCardCommandResponse(sent)
}

func approvedCardCommandResponse(sent archive.ApprovedCardSendResult) (*command.TrawlerCommandResponse, error) {
	if len(sent.Items) != 1 {
		return nil, errors.New("card creation did not return one photo")
	}
	detailDisplayName := "Card created"
	if sent.Items[0].State == "already_created" {
		detailDisplayName = "Card already exists"
	}
	return photosDetailCommandResponse(detailDisplayName,
		photosDetailCanonicalRecordReferenceField("Photo", archive.AssetRef(sent.Items[0].AssetID)),
		photosDetailTextField("Model", sent.Items[0].Model),
		photosDetailTextField("State", sent.Items[0].State)), nil
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
		return "", commandError{Code: "invalid_ref", Message: "ref is not a Photos asset ref"}
	}
	db, err := store.OpenReadOnly(ctx, archivePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	refs, err := (&trawlkit.TrawlerCommandExecutionRequest{OpenedTrawlerArchiveStore: db}).ResolveShortReference(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(ref),
	)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found"}
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandError{Code: "ambiguous_short_ref", Message: "short ref matches more than one asset"}
	}
	if err != nil {
		return "", err
	}
	if len(refs) != 1 {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found"}
	}
	return trawlkit.CanonicalArchiveRecordReferenceText(refs[0]), nil
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
