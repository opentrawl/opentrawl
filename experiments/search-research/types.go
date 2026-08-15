package main

import "time"

const maximumSearchablePassageContentUTF8Bytes = 2000

type registeredTrawlerName string
type canonicalArchiveRecordReference string
type localTrawlerShortReference string
type corpusContentSHA256 string
type embeddingDeploymentSHA256 string
type embeddingPrecision string
type experimentFailureStage string

const (
	float32EmbeddingPrecision  embeddingPrecision     = "float32"
	corpusProjectionFailure    experimentFailureStage = "corpus_projection"
	corpusChunkingFailure      experimentFailureStage = "corpus_chunking"
	embeddingRequestFailure    experimentFailureStage = "embedding_request"
	vectorStorageFailure       experimentFailureStage = "vector_storage"
	candidateGenerationFailure experimentFailureStage = "candidate_generation"
)

type embeddingDeploymentConfiguration struct {
	modelArtifactName        string
	modelArtifactSHA256      string
	modelArtifactBytes       int64
	runtimeName              string
	runtimeVersion           string
	runtimeEndpoint          string
	documentInputPrefix      string
	queryInputPrefix         string
	maximumInputTokens       int
	embeddingDimensions      int
	storedEmbeddingPrecision embeddingPrecision
}

type experimentRunMeasurement struct {
	operationName                     string
	startedAt                         time.Time
	completedAt                       time.Time
	wallElapsed                       time.Duration
	inferenceElapsed                  time.Duration
	storageElapsed                    time.Duration
	modelArtifactBytes                int64
	indexBytes                        int64
	runtimeRootProcessIdentifier      int
	baselineHarnessProcessMemoryBytes int64
	baselineRuntimeProcessMemoryBytes int64
	peakProcessMemoryBytes            int64
	steadyProcessMemoryBytes          int64
	promptTokens                      int64
	candidateGenerationElapsed        time.Duration
	orderingElapsed                   time.Duration
	failureStage                      experimentFailureStage
	failureMessage                    string
}
