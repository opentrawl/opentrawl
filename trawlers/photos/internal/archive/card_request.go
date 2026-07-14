package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	repoPrompts "github.com/opentrawl/opentrawl/trawlers/photos/prompts"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	errUnknownCardCandidate = errors.New("unknown card candidate")
	errPreparedCardMismatch = errors.New("prepared card request mismatch")
)

type preparedCardRequest struct {
	Input           cardinput.Result
	Custody         *cardwire.CardExecutionCustody
	CustodyBytes    []byte
	CustodySHA256   string
	Image           imageMeta
	UTI             string
	MIMEType        string
	PromptVersion   string
	ParserVersion   string
	Request         model.ProviderRequest
	RequestSHA256   string
	CardRequestID   string
	CandidateByID   map[string]preparedPlaceCandidate
	CandidatesInSeq []preparedPlaceCandidate
}

type preparedPlaceCandidate struct {
	ID                string
	Provider          string
	RawResponseID     string
	ProviderIndex     int32
	ProviderID        string
	Name              string
	Categories        []string
	DistanceMeters    float64
	Source            string
	Coordinate        *cardwire.Coordinate
	Address           *cardwire.Address
	PlacePosition     int
	CandidatePosition int
}

func validatePreparedCardRequest(prepared preparedCardRequest) error {
	if prepared.PromptVersion != modelPromptVersion || prepared.ParserVersion != modelParserVersion {
		return fmt.Errorf("%w: unsupported photo card versions", errPreparedCardMismatch)
	}
	input, err := validateCanonicalCardInput(prepared.Input.ID, prepared.Input.Bytes)
	if err != nil {
		return fmt.Errorf("%w: %v", errPreparedCardMismatch, err)
	}
	if prepared.Custody == nil {
		return fmt.Errorf("%w: custody is required", errPreparedCardMismatch)
	}
	custodyBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(prepared.Custody)
	if err != nil || !bytes.Equal(custodyBytes, prepared.CustodyBytes) {
		return fmt.Errorf("%w: custody bytes", errPreparedCardMismatch)
	}
	custodyDigest := sha256.Sum256(custodyBytes)
	if hex.EncodeToString(custodyDigest[:]) != prepared.CustodySHA256 {
		return fmt.Errorf("%w: custody digest", errPreparedCardMismatch)
	}
	requestDigest := prepared.Request.Digest()
	requestSHA256 := hex.EncodeToString(requestDigest[:])
	if requestSHA256 != prepared.RequestSHA256 {
		return fmt.Errorf("%w: request digest", errPreparedCardMismatch)
	}
	if err := validateCustodyForInput(prepared.Custody, input, requestSHA256); err != nil {
		return err
	}
	fullCurrent := input.Input.GetFullCurrent()
	if fullCurrent.GetSha256() != prepared.Image.SHA256 || fullCurrent.GetSizeBytes() != prepared.Image.Bytes ||
		fullCurrent.GetMediaType() != prepared.UTI {
		return fmt.Errorf("%w: full-current facts", errPreparedCardMismatch)
	}
	mimeType, err := currentStillMIMEType(prepared.UTI)
	if err != nil || mimeType != prepared.MIMEType {
		return fmt.Errorf("%w: media type binding", errPreparedCardMismatch)
	}
	registry, ordered, err := candidateRegistry(input.Input)
	if err != nil || !preparedCandidatesEqual(registry, ordered, prepared.CandidateByID, prepared.CandidatesInSeq) {
		return fmt.Errorf("%w: candidate registry", errPreparedCardMismatch)
	}
	if prepared.CardRequestID != cardRequestID(input.ID, prepared.UTI, prepared.MIMEType, prepared.PromptVersion, prepared.Request) {
		return fmt.Errorf("%w: card request identity", errPreparedCardMismatch)
	}
	return nil
}

func preparedCandidatesEqual(leftMap map[string]preparedPlaceCandidate, left []preparedPlaceCandidate, rightMap map[string]preparedPlaceCandidate, right []preparedPlaceCandidate) bool {
	return reflect.DeepEqual(leftMap, rightMap) && reflect.DeepEqual(left, right)
}

func renderPreparedCardRequest(source cardinput.SourceFacts, artifacts cardinput.CheckedArtifacts, evidence []place.EvidenceRecord, image []byte, classifier modelClassifier) (preparedCardRequest, error) {
	input, err := cardinput.Build(source, artifacts, evidence)
	if err != nil {
		return preparedCardRequest{}, err
	}
	custody := executionCustody(source, artifacts, evidence)
	return renderPreparedCardRequestFromBytes(input.ID, input.Bytes, custody, image, classifier)
}

func renderPreparedCardRequestFromBytes(cardInputID string, cardInputBytes []byte, custody *cardwire.CardExecutionCustody, image []byte, classifier modelClassifier) (preparedCardRequest, error) {
	if classifier.promptVersion != modelPromptVersion {
		return preparedCardRequest{}, fmt.Errorf("photo card prompt version %q is not registered", classifier.promptVersion)
	}
	if classifier.client == nil {
		return preparedCardRequest{}, errors.New("model client is required")
	}
	input, err := validateCanonicalCardInput(cardInputID, cardInputBytes)
	if err != nil {
		return preparedCardRequest{}, err
	}
	if custody == nil {
		return preparedCardRequest{}, errors.New("card custody is required")
	}
	if err := validateCustodyForInput(custody, input, ""); err != nil {
		return preparedCardRequest{}, err
	}
	if len(image) == 0 {
		return preparedCardRequest{}, errors.New("checked full-current image is required")
	}
	imageDigest := sha256.Sum256(image)
	if int64(len(image)) != input.Input.GetFullCurrent().GetSizeBytes() ||
		hex.EncodeToString(imageDigest[:]) != input.Input.GetFullCurrent().GetSha256() {
		return preparedCardRequest{}, errors.New("checked image bytes do not match CardInput full-current facts")
	}
	mimeType, err := currentStillMIMEType(input.Input.GetFullCurrent().GetMediaType())
	if err != nil {
		return preparedCardRequest{}, err
	}
	registry, ordered, err := candidateRegistry(input.Input)
	if err != nil {
		return preparedCardRequest{}, err
	}
	prompt, err := renderCardInputPrompt(input.Input)
	if err != nil {
		return preparedCardRequest{}, err
	}
	request := model.Request{
		Prompt:      prompt,
		Images:      []model.Image{{Data: bytes.Clone(image), MIMEType: mimeType}},
		Temperature: 0.1,
	}
	rendered, err := classifier.client.Render(request)
	if err != nil {
		return preparedCardRequest{}, err
	}
	if err := classifier.client.ValidateRequest(rendered); err != nil {
		return preparedCardRequest{}, err
	}
	requestDigest := rendered.Digest()
	requestSHA256 := hex.EncodeToString(requestDigest[:])
	finalCustody, custodyBytes, custodySHA256, err := marshalCustody(custody, input.Bytes, requestSHA256)
	if err != nil {
		return preparedCardRequest{}, err
	}
	prepared := preparedCardRequest{
		Input:           input,
		Custody:         finalCustody,
		CustodyBytes:    custodyBytes,
		CustodySHA256:   custodySHA256,
		Image:           imageMeta{Bytes: int64(len(image)), SHA256: hex.EncodeToString(imageDigest[:])},
		UTI:             input.Input.GetFullCurrent().GetMediaType(),
		MIMEType:        mimeType,
		PromptVersion:   classifier.promptVersion,
		ParserVersion:   modelParserVersion,
		Request:         rendered,
		RequestSHA256:   requestSHA256,
		CardRequestID:   cardRequestID(input.ID, input.Input.GetFullCurrent().GetMediaType(), mimeType, classifier.promptVersion, rendered),
		CandidateByID:   registry,
		CandidatesInSeq: ordered,
	}
	if err := validatePreparedCardRequest(prepared); err != nil {
		return preparedCardRequest{}, err
	}
	return prepared, nil
}

func restorePreparedCardRequest(item *cardwire.ApprovedCardItem, client *model.Client) (preparedCardRequest, error) {
	if client == nil {
		return preparedCardRequest{}, errors.New("model client is required to restore a prepared card request")
	}
	prepared, err := restorePreparedCardRequestUnchecked(item)
	if err != nil {
		return preparedCardRequest{}, err
	}
	if err := client.ValidateRequest(prepared.Request); err != nil {
		return preparedCardRequest{}, err
	}
	return prepared, nil
}

// restorePreparedCardRequestUnchecked proves the retained bytes and their
// cross-links. Callers must validate the restored request against their
// configured transport before any send.
func restorePreparedCardRequestUnchecked(item *cardwire.ApprovedCardItem) (preparedCardRequest, error) {
	if item == nil {
		return preparedCardRequest{}, errors.New("approved card item is required")
	}
	if item.GetPromptVersion() != modelPromptVersion {
		return preparedCardRequest{}, fmt.Errorf("photo card prompt version %q is not registered", item.GetPromptVersion())
	}
	input, err := validateCanonicalCardInput(item.GetCardInputId(), item.GetCardInput())
	if err != nil {
		return preparedCardRequest{}, err
	}
	custody := new(cardwire.CardExecutionCustody)
	if err := proto.Unmarshal(item.GetCustody(), custody); err != nil {
		return preparedCardRequest{}, fmt.Errorf("decode approved custody: %w", err)
	}
	request, err := model.RestoreProviderRequest(item.GetRequestRoute(), item.GetModelId(), item.GetRequestBody())
	if err != nil {
		return preparedCardRequest{}, err
	}
	requestDigest := request.Digest()
	requestSHA256 := hex.EncodeToString(requestDigest[:])
	if item.GetRequestSha256() != requestSHA256 {
		return preparedCardRequest{}, errors.New("approved request digest does not match its bytes")
	}
	if err := validateCustodyForInput(custody, input, requestSHA256); err != nil {
		return preparedCardRequest{}, err
	}
	finalCustody, custodyBytes, custodySHA256, err := marshalCustody(custody, input.Bytes, requestSHA256)
	if err != nil {
		return preparedCardRequest{}, err
	}
	if custodySHA256 != item.GetCustodySha256() || !bytes.Equal(custodyBytes, item.GetCustody()) {
		return preparedCardRequest{}, errors.New("approved custody digest does not match its bytes")
	}
	registry, ordered, err := candidateRegistry(input.Input)
	if err != nil {
		return preparedCardRequest{}, err
	}
	mimeType, err := currentStillMIMEType(input.Input.GetFullCurrent().GetMediaType())
	if err != nil {
		return preparedCardRequest{}, err
	}
	prepared := preparedCardRequest{
		Input:           input,
		Custody:         finalCustody,
		CustodyBytes:    custodyBytes,
		CustodySHA256:   custodySHA256,
		Image:           imageMeta{Bytes: input.Input.GetFullCurrent().GetSizeBytes(), SHA256: input.Input.GetFullCurrent().GetSha256()},
		UTI:             input.Input.GetFullCurrent().GetMediaType(),
		MIMEType:        mimeType,
		PromptVersion:   item.GetPromptVersion(),
		ParserVersion:   item.GetParserVersion(),
		Request:         request,
		RequestSHA256:   requestSHA256,
		CardRequestID:   cardRequestID(input.ID, input.Input.GetFullCurrent().GetMediaType(), mimeType, item.GetPromptVersion(), request),
		CandidateByID:   registry,
		CandidatesInSeq: ordered,
	}
	if err := validatePreparedCardRequest(prepared); err != nil {
		return preparedCardRequest{}, err
	}
	return prepared, nil
}

func validateCanonicalCardInput(cardInputID string, data []byte) (cardinput.Result, error) {
	if len(data) == 0 {
		return cardinput.Result{}, errors.New("CardInput bytes are required")
	}
	input := new(cardwire.CardInput)
	if err := proto.Unmarshal(data, input); err != nil {
		return cardinput.Result{}, fmt.Errorf("decode CardInput: %w", err)
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(input)
	if err != nil {
		return cardinput.Result{}, fmt.Errorf("marshal CardInput: %w", err)
	}
	if !bytes.Equal(data, canonical) {
		return cardinput.Result{}, errors.New("CardInput bytes are not canonical")
	}
	digest := sha256.Sum256(data)
	wantID := "card_input:" + hex.EncodeToString(digest[:])
	if cardInputID != wantID {
		return cardinput.Result{}, errors.New("CardInput identity does not match its bytes")
	}
	if input.GetSchemaVersion() != cardinput.SchemaVersion {
		return cardinput.Result{}, errors.New("CardInput schema version is not supported")
	}
	if input.GetFullCurrent() == nil {
		return cardinput.Result{}, errors.New("CardInput full-current facts are required")
	}
	return cardinput.Result{Input: input, Bytes: bytes.Clone(data), ID: cardInputID}, nil
}

func validateCustodyForInput(custody *cardwire.CardExecutionCustody, input cardinput.Result, requestSHA256 string) error {
	if custody.GetAssetId() == "" || custody.GetSourceId() == "" {
		return errors.New("custody source and asset ids are required")
	}
	inputDigest := sha256.Sum256(input.Bytes)
	if custody.GetCardInputSha256() != "" && custody.GetCardInputSha256() != hex.EncodeToString(inputDigest[:]) {
		return fmt.Errorf("%w: custody CardInput digest", errPreparedCardMismatch)
	}
	if requestSHA256 != "" && custody.GetRequestSha256() != requestSHA256 {
		return fmt.Errorf("%w: custody request digest", errPreparedCardMismatch)
	}
	if custody.GetFullCurrentProofSha256() == "" {
		return errors.New("custody full-current proof is required")
	}
	return nil
}

func marshalCustody(custody *cardwire.CardExecutionCustody, inputBytes []byte, requestSHA256 string) (*cardwire.CardExecutionCustody, []byte, string, error) {
	copy := proto.Clone(custody).(*cardwire.CardExecutionCustody)
	inputDigest := sha256.Sum256(inputBytes)
	copy.CardInputSha256 = hex.EncodeToString(inputDigest[:])
	copy.RequestSha256 = requestSHA256
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(copy)
	if err != nil {
		return nil, nil, "", fmt.Errorf("marshal card custody: %w", err)
	}
	digest := sha256.Sum256(data)
	return copy, data, hex.EncodeToString(digest[:]), nil
}

func candidateRegistry(input *cardwire.CardInput) (map[string]preparedPlaceCandidate, []preparedPlaceCandidate, error) {
	registry := map[string]preparedPlaceCandidate{}
	ordered := []preparedPlaceCandidate{}
	for placeIndex, projection := range input.GetPlaces() {
		for candidateIndex, candidate := range projection.GetCandidates() {
			id := candidate.GetCandidateId()
			want := fmt.Sprintf("place_%d_candidate_%d", placeIndex+1, candidateIndex+1)
			if id != want {
				return nil, nil, fmt.Errorf("CardInput candidate id %q does not match %q", id, want)
			}
			if _, exists := registry[id]; exists {
				return nil, nil, fmt.Errorf("CardInput candidate id %q is duplicated", id)
			}
			row := preparedPlaceCandidate{
				ID:                id,
				Provider:          projection.GetProviderIdentity(),
				RawResponseID:     projection.GetRawResponseSha256(),
				ProviderIndex:     candidate.GetProviderIndex(),
				ProviderID:        candidate.GetProviderId(),
				Name:              candidate.GetName(),
				Categories:        append([]string(nil), candidate.GetCategories()...),
				DistanceMeters:    candidate.GetDistanceMeters(),
				Source:            candidate.GetSource(),
				Coordinate:        candidate.GetCoordinate(),
				Address:           candidate.GetAddress(),
				PlacePosition:     placeIndex + 1,
				CandidatePosition: candidateIndex + 1,
			}
			registry[id] = row
			ordered = append(ordered, row)
		}
	}
	return registry, ordered, nil
}

func renderCardInputPrompt(input *cardwire.CardInput) (string, error) {
	metadataJSON, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true, Indent: "  "}.Marshal(input)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("photo-card").Option("missingkey=error").Parse(repoPrompts.PhotoCardV3)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, map[string]string{"MetadataJSON": string(metadataJSON)}); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func cardRequestID(cardInputID, uti, mimeType, promptVersion string, request model.ProviderRequest) string {
	hash := sha256.New()
	requestDigest := request.Digest()
	for _, part := range [][]byte{
		[]byte(cardInputID),
		[]byte(uti),
		[]byte(mimeType),
		[]byte(promptVersion),
		[]byte(request.Route()),
		[]byte(request.Model()),
		request.Body(),
		requestDigest[:],
	} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(part)
	}
	return "card_request:" + hex.EncodeToString(hash.Sum(nil))
}
