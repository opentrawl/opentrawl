//go:build darwin

package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	"google.golang.org/protobuf/proto"
)

const (
	installedOpenTrawlApplicationPath = "/Applications/OpenTrawl.app"
	maximumMediaWireBytes             = 1 * 1024 * 1024
	defaultMediaOperationTimeout      = 2 * time.Minute
	photosMediaClientLockFilename     = "client.lock"
)

type Client struct {
	applicationPath string
	timeout         time.Duration
}

type CurrentRenderedStillLease struct {
	Outcome *mediawire.CurrentRenderedStillLease
	session *requestSession
	client  Client
	once    sync.Once
	err     error
}

type PhotosMediaOutcomeError struct {
	Unavailable       *mediawire.PhotosMediaUnavailable
	AdmissionDeferred *mediawire.PhotosMediaAdmissionDeferred
	OperationFailure  *mediawire.PhotosMediaOperationFailure
}

func (e *PhotosMediaOutcomeError) Error() string {
	switch {
	case e == nil:
		return ""
	case e.Unavailable != nil:
		return e.Unavailable.GetHumanDescription()
	case e.AdmissionDeferred != nil:
		return e.AdmissionDeferred.GetHumanDescription()
	case e.OperationFailure != nil:
		return e.OperationFailure.GetHumanDescription()
	default:
		return "OpenTrawl returned no Photos media outcome"
	}
}

func NewInstalledOpenTrawlClient() Client {
	return Client{applicationPath: installedOpenTrawlApplicationPath, timeout: defaultMediaOperationTimeout}
}

func (c Client) ReadPhotoLibraryAccess(ctx context.Context) (*mediawire.PhotoLibraryAccessResult, error) {
	request := &mediawire.PhotosMediaRequest{Operation: &mediawire.PhotosMediaRequest_ReadPhotoLibraryAccess{
		ReadPhotoLibraryAccess: &mediawire.ReadPhotoLibraryAccessRequest{},
	}}
	response, err := c.performOne(ctx, request)
	if err != nil {
		return nil, err
	}
	if outcome := response.GetPhotoLibraryAccess(); outcome != nil {
		return outcome, nil
	}
	return nil, outcomeError(response)
}

func (c Client) RequestPhotoLibraryAccess(ctx context.Context) (*mediawire.PhotoLibraryAccessResult, error) {
	request := &mediawire.PhotosMediaRequest{Operation: &mediawire.PhotosMediaRequest_RequestPhotoLibraryAccess{
		RequestPhotoLibraryAccess: &mediawire.RequestPhotoLibraryAccessRequest{},
	}}
	response, err := c.performOne(ctx, request)
	if err != nil {
		return nil, err
	}
	if outcome := response.GetPhotoLibraryAccess(); outcome != nil {
		return outcome, nil
	}
	return nil, outcomeError(response)
}

func (c Client) InspectPhotoAssetReadiness(ctx context.Context, localIdentifier string) (*mediawire.PhotoAssetReadiness, error) {
	request := &mediawire.PhotosMediaRequest{Operation: &mediawire.PhotosMediaRequest_InspectPhotoAssetReadiness{
		InspectPhotoAssetReadiness: &mediawire.InspectPhotoAssetReadinessRequest{PhotoAssetLocalIdentifier: localIdentifier},
	}}
	response, err := c.performOne(ctx, request)
	if err != nil {
		return nil, err
	}
	if outcome := response.GetPhotoAssetReadiness(); outcome != nil {
		return outcome, nil
	}
	return nil, outcomeError(response)
}

func (c Client) InspectImmutableOriginalImageFacts(
	ctx context.Context,
	request *mediawire.InspectImmutableOriginalImageFactsRequest,
) (*mediawire.ImmutableOriginalImageFacts, error) {
	response, err := c.performOne(ctx, &mediawire.PhotosMediaRequest{Operation: &mediawire.PhotosMediaRequest_InspectImmutableOriginalImageFacts{
		InspectImmutableOriginalImageFacts: request,
	}})
	if err != nil {
		return nil, err
	}
	if outcome := response.GetImmutableOriginalImageFacts(); outcome != nil {
		return outcome, nil
	}
	return nil, outcomeError(response)
}

func (c Client) AcquireCurrentRenderedStill(
	ctx context.Context,
	request *mediawire.AcquireCurrentRenderedStillRequest,
) (*CurrentRenderedStillLease, error) {
	session, err := c.newSession()
	if err != nil {
		return nil, err
	}
	response, err := c.perform(ctx, session, &mediawire.PhotosMediaRequest{Operation: &mediawire.PhotosMediaRequest_AcquireCurrentRenderedStill{
		AcquireCurrentRenderedStill: request,
	}})
	if err != nil {
		session.remove()
		return nil, err
	}
	outcome := response.GetCurrentRenderedStillLease()
	if outcome == nil {
		session.remove()
		return nil, outcomeError(response)
	}
	if err := session.verifyCurrentRenderedStillLease(outcome); err != nil {
		session.remove()
		return nil, err
	}
	return &CurrentRenderedStillLease{Outcome: outcome, session: session, client: c}, nil
}

func (l *CurrentRenderedStillLease) Read() ([]byte, error) {
	if l == nil || l.Outcome == nil {
		return nil, errors.New("current rendered still lease is not available")
	}
	data, err := os.ReadFile(l.Outcome.GetCheckedFilePath())
	if err != nil {
		return nil, fmt.Errorf("read checked current rendered still: %w", err)
	}
	digest := sha256.Sum256(data)
	if uint64(len(data)) != l.Outcome.GetByteCount() || !bytes.Equal(digest[:], l.Outcome.GetSha256()) {
		return nil, errors.New("checked current rendered still changed while leased")
	}
	return data, nil
}

// Verify checks that the installed application's leased bytes still match its
// typed outcome. Consumers that need the image call Read instead.
func (l *CurrentRenderedStillLease) Verify() error {
	_, err := l.Read()
	return err
}

func (l *CurrentRenderedStillLease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		defer l.session.remove()
		response, err := l.client.perform(context.Background(), l.session, &mediawire.PhotosMediaRequest{Operation: &mediawire.PhotosMediaRequest_ReleaseCurrentRenderedStillLease{
			ReleaseCurrentRenderedStillLease: &mediawire.ReleaseCurrentRenderedStillLeaseRequest{LeaseIdentifier: l.Outcome.GetLeaseIdentifier()},
		}})
		if err != nil {
			l.err = err
			return
		}
		released := response.GetReleasedCurrentRenderedStillLease()
		if released == nil || released.GetLeaseIdentifier() != l.Outcome.GetLeaseIdentifier() {
			l.err = outcomeError(response)
		}
	})
	return l.err
}

func (c Client) performOne(ctx context.Context, request *mediawire.PhotosMediaRequest) (*mediawire.PhotosMediaResponse, error) {
	session, err := c.newSession()
	if err != nil {
		return nil, err
	}
	defer session.remove()
	return c.perform(ctx, session, request)
}

func (c Client) perform(ctx context.Context, session *requestSession, request *mediawire.PhotosMediaRequest) (*mediawire.PhotosMediaResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := proto.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode OpenTrawl Photos media request: %w", err)
	}
	if len(data) == 0 || len(data) > maximumMediaWireBytes {
		return nil, errors.New("OpenTrawl Photos media request exceeds the typed wire limit")
	}
	if err := os.Remove(session.responsePath()); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove previous OpenTrawl Photos media response: %w", err)
	}
	if err := os.WriteFile(session.requestPath(), data, 0o600); err != nil {
		return nil, fmt.Errorf("write OpenTrawl Photos media request: %w", err)
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultMediaOperationTimeout
	}
	operationContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(operationContext, "/usr/bin/open", "-g", "-a", c.applicationPath, session.requestPath())
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("open installed OpenTrawl for Photos media: %w: %s", err, bytes.TrimSpace(output))
	}
	responseData, err := waitForResponse(operationContext, session.responsePath())
	if err != nil {
		return nil, err
	}
	var response mediawire.PhotosMediaResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		return nil, fmt.Errorf("decode OpenTrawl Photos media response: %w", err)
	}
	return &response, nil
}

func (c Client) newSession() (*requestSession, error) {
	if c.applicationPath == "" {
		c.applicationPath = installedOpenTrawlApplicationPath
	}
	info, err := os.Stat(c.applicationPath)
	if err != nil || !info.IsDir() {
		return nil, errors.New("OpenTrawl must be installed in Applications before Photos media can be read")
	}
	directory, err := os.MkdirTemp("", "opentrawl-photos-media-")
	if err != nil {
		return nil, fmt.Errorf("create OpenTrawl Photos media IPC directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		return nil, fmt.Errorf("protect OpenTrawl Photos media IPC directory: %w", err)
	}
	clientLock, err := os.OpenFile(filepath.Join(directory, photosMediaClientLockFilename), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, fmt.Errorf("create OpenTrawl Photos media client lock: %w", err)
	}
	clientWriteLock := syscall.Flock_t{
		Type:   syscall.F_WRLCK,
		Whence: int16(io.SeekStart),
	}
	if err := syscall.FcntlFlock(clientLock.Fd(), syscall.F_SETLKW, &clientWriteLock); err != nil {
		_ = clientLock.Close()
		_ = os.RemoveAll(directory)
		return nil, fmt.Errorf("hold OpenTrawl Photos media client lock: %w", err)
	}
	return &requestSession{directory: directory, clientLock: clientLock}, nil
}

type requestSession struct {
	directory  string
	clientLock *os.File
	removeOnce sync.Once
}

func (s requestSession) requestPath() string {
	return filepath.Join(s.directory, "request.opentrawl-photos-media-request")
}

func (s requestSession) responsePath() string {
	return filepath.Join(s.directory, "request.response.pb")
}

func (s *requestSession) remove() {
	if s == nil {
		return
	}
	s.removeOnce.Do(func() {
		if s.clientLock != nil {
			clientUnlock := syscall.Flock_t{
				Type:   syscall.F_UNLCK,
				Whence: int16(io.SeekStart),
			}
			_ = syscall.FcntlFlock(s.clientLock.Fd(), syscall.F_SETLK, &clientUnlock)
			_ = s.clientLock.Close()
		}
		_ = os.RemoveAll(s.directory)
	})
}

func (s *requestSession) verifyCurrentRenderedStillLease(lease *mediawire.CurrentRenderedStillLease) error {
	if lease.GetLeaseIdentifier() == "" || lease.GetCheckedFilePath() == "" {
		return errors.New("installed OpenTrawl returned an incomplete current rendered still lease")
	}
	expectedPath := filepath.Join(s.directory, lease.GetLeaseIdentifier()+".image")
	actualPath, err := filepath.EvalSymlinks(lease.GetCheckedFilePath())
	if err != nil {
		return fmt.Errorf("resolve checked current rendered still lease: %w", err)
	}
	expectedPath, err = filepath.EvalSymlinks(expectedPath)
	if err != nil {
		return fmt.Errorf("resolve expected current rendered still lease: %w", err)
	}
	if actualPath != expectedPath {
		return errors.New("installed OpenTrawl returned a current rendered still outside its IPC directory")
	}
	data, err := os.ReadFile(actualPath)
	if err != nil {
		return fmt.Errorf("read checked current rendered still lease: %w", err)
	}
	digest := sha256.Sum256(data)
	if uint64(len(data)) != lease.GetByteCount() || !bytes.Equal(digest[:], lease.GetSha256()) {
		return errors.New("installed OpenTrawl returned a current rendered still with mismatched proof")
	}
	return nil
}

func waitForResponse(ctx context.Context, path string) ([]byte, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			if len(data) == 0 || len(data) > maximumMediaWireBytes {
				return nil, errors.New("installed OpenTrawl returned an invalid Photos media response size")
			}
			return data, nil
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read installed OpenTrawl Photos media response: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func outcomeError(response *mediawire.PhotosMediaResponse) error {
	if response == nil {
		return errors.New("installed OpenTrawl returned no Photos media response")
	}
	return &PhotosMediaOutcomeError{
		Unavailable:       response.GetUnavailable(),
		AdmissionDeferred: response.GetAdmissionDeferred(),
		OperationFailure:  response.GetOperationFailure(),
	}
}
