package updatephotos

import (
	"errors"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	photosmedia "github.com/opentrawl/opentrawl/trawlers/photos/internal/media"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
)

const observationInterval = 30 * time.Second

type WorkDisposition uint8

const (
	WorkAcquired WorkDisposition = iota + 1
	WorkReused
	WorkSkipped
	WorkRetried
	WorkDeferred
	WorkFailed
)

func (disposition WorkDisposition) String() string {
	switch disposition {
	case WorkAcquired:
		return "acquired"
	case WorkReused:
		return "reused"
	case WorkSkipped:
		return "skipped"
	case WorkRetried:
		return "retried"
	case WorkDeferred:
		return "deferred"
	case WorkFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type WorkCount struct {
	Node        ProductionNodeName
	Disposition WorkDisposition
	Count       uint64
}

type OperationalSnapshot struct {
	Completed, Total, Active, Capacity int
	OldestAssetID                      archive.PhotoAssetID
	OldestNode                         ProductionNodeName
	OldestInFlight                     time.Duration
	ActiveMediaLeases                  int
	LeasedMediaBytes                   uint64
	Counts                             []WorkCount
}

type WorkIncident struct {
	AssetID               archive.PhotoAssetID
	Node                  ProductionNodeName
	Disposition           WorkDisposition
	Duration              time.Duration
	MediaDeferralReason   mediawire.PhotosMediaAdmissionDeferralReason
	MediaOperationFailure mediawire.PhotosMediaOperationFailureKind
}

type Observation interface{ isObservation() }

func (OperationalSnapshot) isObservation() {}
func (WorkIncident) isObservation()        {}

type workKey struct {
	assetID archive.PhotoAssetID
	node    ProductionNodeName
}

type observationAccumulator struct {
	mu             sync.Mutex
	observe        func(Observation)
	activeAssets   map[archive.PhotoAssetID]struct{}
	activeNodes    map[workKey]time.Time
	mediaLeaseByte map[archive.PhotoAssetID]uint64
	counts         map[WorkCount]uint64
}

func newObservationAccumulator(observe func(Observation)) *observationAccumulator {
	return &observationAccumulator{observe: observe, activeAssets: map[archive.PhotoAssetID]struct{}{}, activeNodes: map[workKey]time.Time{}, mediaLeaseByte: map[archive.PhotoAssetID]uint64{}, counts: map[WorkCount]uint64{}}
}

func (observations *observationAccumulator) startAsset(assetID archive.PhotoAssetID) {
	observations.mu.Lock()
	observations.activeAssets[assetID] = struct{}{}
	observations.mu.Unlock()
}

func (observations *observationAccumulator) finishAsset(assetID archive.PhotoAssetID) {
	observations.mu.Lock()
	delete(observations.activeAssets, assetID)
	observations.mu.Unlock()
}

func (observations *observationAccumulator) startNode(assetID archive.PhotoAssetID, node ProductionNodeName) {
	observations.mu.Lock()
	observations.activeNodes[workKey{assetID, node}] = time.Now()
	observations.mu.Unlock()
}

func (observations *observationAccumulator) finishNode(assetID archive.PhotoAssetID, node ProductionNodeName, disposition WorkDisposition, mediaErr *mediawire.PhotosMediaOperationFailure, deferred *mediawire.PhotosMediaAdmissionDeferred) {
	observations.mu.Lock()
	key := workKey{assetID, node}
	startedAt := observations.activeNodes[key]
	delete(observations.activeNodes, key)
	observations.mu.Unlock()
	duration := time.Duration(0)
	if !startedAt.IsZero() {
		duration = time.Since(startedAt)
	}
	observations.recordWithDuration(assetID, node, disposition, duration, mediaErr, deferred)
}

func (observations *observationAccumulator) record(assetID archive.PhotoAssetID, node ProductionNodeName, disposition WorkDisposition, mediaErr *mediawire.PhotosMediaOperationFailure, deferred *mediawire.PhotosMediaAdmissionDeferred) {
	observations.recordWithDuration(assetID, node, disposition, 0, mediaErr, deferred)
}

func (observations *observationAccumulator) recordWithDuration(assetID archive.PhotoAssetID, node ProductionNodeName, disposition WorkDisposition, duration time.Duration, mediaErr *mediawire.PhotosMediaOperationFailure, deferred *mediawire.PhotosMediaAdmissionDeferred) {
	if disposition == 0 {
		return
	}
	key := WorkCount{Node: node, Disposition: disposition}
	observations.mu.Lock()
	observations.counts[key]++
	observe := observations.observe
	observations.mu.Unlock()
	if observe != nil && disposition >= WorkRetried {
		incident := WorkIncident{AssetID: assetID, Node: node, Disposition: disposition, Duration: duration}
		if mediaErr != nil {
			incident.MediaOperationFailure = mediaErr.GetKind()
		}
		if deferred != nil {
			incident.MediaDeferralReason = deferred.GetReason()
		}
		observe(incident)
	}
}

func (observations *observationAccumulator) acquireMediaLease(assetID archive.PhotoAssetID, bytes uint64) {
	observations.mu.Lock()
	observations.mediaLeaseByte[assetID] = bytes
	observations.mu.Unlock()
}

func (observations *observationAccumulator) releaseMediaLease(assetID archive.PhotoAssetID) {
	observations.mu.Lock()
	delete(observations.mediaLeaseByte, assetID)
	observations.mu.Unlock()
}

func (observations *observationAccumulator) snapshot(completed, total, capacity int) {
	observations.mu.Lock()
	snapshot := OperationalSnapshot{Completed: completed, Total: total, Active: len(observations.activeAssets), Capacity: capacity, ActiveMediaLeases: len(observations.mediaLeaseByte)}
	now := time.Now()
	for key, startedAt := range observations.activeNodes {
		if age := now.Sub(startedAt); age > snapshot.OldestInFlight {
			snapshot.OldestAssetID, snapshot.OldestNode, snapshot.OldestInFlight = key.assetID, key.node, age
		}
	}
	for _, bytes := range observations.mediaLeaseByte {
		snapshot.LeasedMediaBytes += bytes
	}
	for _, node := range productionNodesInDependencyOrder {
		for disposition := WorkAcquired; disposition <= WorkFailed; disposition++ {
			key := WorkCount{Node: node.Name, Disposition: disposition}
			if count := observations.counts[key]; count > 0 {
				key.Count = count
				snapshot.Counts = append(snapshot.Counts, key)
			}
		}
	}
	observe := observations.observe
	observations.mu.Unlock()
	if observe != nil {
		observe(snapshot)
	}
}

func (runner *Runner) finishObservedNode(assetID archive.PhotoAssetID, node ProductionNodeName, operationErr error, successfulWorkWasAcquired bool) {
	if operationErr == nil {
		if successfulWorkWasAcquired {
			runner.observations.finishNode(assetID, node, WorkAcquired, nil, nil)
		} else {
			runner.observations.finishNode(assetID, node, 0, nil, nil)
		}
		return
	}
	var mediaOutcome *photosmedia.PhotosMediaOutcomeError
	if errors.As(operationErr, &mediaOutcome) {
		switch {
		case mediaOutcome.AdmissionDeferred != nil:
			runner.observations.finishNode(assetID, node, WorkDeferred, nil, mediaOutcome.AdmissionDeferred)
		case mediaOutcome.OperationFailure != nil:
			runner.observations.finishNode(assetID, node, WorkFailed, mediaOutcome.OperationFailure, nil)
		default:
			runner.observations.finishNode(assetID, node, WorkSkipped, nil, nil)
		}
		return
	}
	var deferred *AssetDeferredError
	if errors.As(operationErr, &deferred) {
		runner.observations.finishNode(assetID, node, WorkDeferred, nil, nil)
		return
	}
	runner.observations.finishNode(assetID, node, WorkFailed, nil, nil)
}

func (runner *Runner) closeCurrentRenderedStill(assetID archive.PhotoAssetID, lease *photosmedia.CurrentRenderedStillLease) {
	if lease == nil {
		return
	}
	err := lease.Close()
	runner.observations.releaseMediaLease(assetID)
	if err != nil {
		runner.observations.record(assetID, ProductionNodeCurrentMedia, WorkFailed, nil, nil)
	}
}

func (runner *Runner) finishLocationProviderNode(assetID archive.PhotoAssetID, node ProductionNodeName, outcome any, operationErr error) {
	if operationErr != nil {
		runner.observations.finishNode(assetID, node, WorkFailed, nil, nil)
		return
	}
	var exchange *locationwire.ProviderExchange
	var evidenceUse locationwire.ProviderEvidenceUse
	switch typed := outcome.(type) {
	case *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome:
		exchange, evidenceUse = typed.GetExchange(), typed.GetEvidenceUse()
	case *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome:
		exchange, evidenceUse = typed.GetExchange(), typed.GetEvidenceUse()
	case *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome:
		exchange, evidenceUse = typed.GetExchange(), typed.GetEvidenceUse()
	}
	switch {
	case exchange == nil:
		runner.observations.finishNode(assetID, node, WorkFailed, nil, nil)
	case exchange.GetState() == locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
		runner.observations.finishNode(assetID, node, WorkSkipped, nil, nil)
	case exchange.GetState() == locationwire.OperationState_OPERATION_STATE_FAILED && providerRetryNotBeforeIsFuture(exchange, time.Now()):
		runner.observations.finishNode(assetID, node, WorkDeferred, nil, nil)
	case exchange.GetState() == locationwire.OperationState_OPERATION_STATE_FAILED:
		runner.observations.finishNode(assetID, node, WorkFailed, nil, nil)
	case evidenceUse == locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED:
		runner.observations.finishNode(assetID, node, WorkReused, nil, nil)
	default:
		runner.observations.finishNode(assetID, node, WorkAcquired, nil, nil)
	}
}
