import Foundation
import SwiftProtobuf
import Testing

@testable import TrawlClient

@Test func canonicalSearchMapsExactTimestampAndReferences() throws {
  var hit = canonicalSearchHit()
  hit.shortRef = "short-1"
  hit.timeRfc3339 = "2026-07-12T09:30:00Z"
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .complete
  response.order = .recency
  response.resultLimit = 20
  response.hits = [hit]
  let model = try response.model()
  #expect(model.hits.first?.openRef == "synthetic:record/full")
  #expect(model.hits.first?.shortRef == "short-1")
  #expect(model.hits.first?.timeRFC3339 == "2026-07-12T09:30:00Z")
}

@Test func canonicalMappingsAcceptOptionalAndFractionalRFC3339Timestamps() throws {
  let timestamps = [
    "", "2026-07-12T21:51:13Z", "2026-07-12T21:51:13.123Z",
    "2026-07-12T21:51:13.123456789Z", "2026-07-12T21:51:13+01:30",
    "2026-07-12T21:51:13.123-01:30", "2026-07-12T21:51:13+23:00",
    "2026-07-12T21:51:13.123-23:59", "0000-01-01T00:00:00Z",
  ]
  for timestamp in timestamps {
    var source = Trawl_Federation_V1_SourceStatus()
    source.manifest = .with {
      $0.sourceID = "synthetic"
      $0.displayName = "Synthetic"
    }
    source.generatedRfc3339 = timestamp
    source.lastSyncRfc3339 = timestamp
    source.lastImportRfc3339 = timestamp
    source.lastExportRfc3339 = timestamp
    source.remote = .with {
      $0.lastIngestRfc3339 = timestamp
      $0.lastSyncRfc3339 = timestamp
    }
    source.databases = [.with { $0.modifiedRfc3339 = timestamp }]
    var status = Trawl_Federation_V1_StatusResponse()
    status.outcome = .complete
    status.sources = [source]
    let mappedStatus = try status.model().sources[0]
    #expect(mappedStatus.generatedRFC3339 == timestamp)
    #expect(mappedStatus.remote?.lastIngestRFC3339 == timestamp)
    #expect(mappedStatus.databases[0].modifiedRFC3339 == timestamp)

    var hit = canonicalSearchHit()
    hit.timeRfc3339 = timestamp
    var search = Trawl_Federation_V1_SearchResponse()
    search.outcome = .complete
    search.order = .recency
    search.hits = [hit]
    let mappedHit = try search.model().hits[0]
    #expect(mappedHit.timeRFC3339 == timestamp)
    #expect(
      (timestamp.isEmpty && mappedHit.time == nil) || (!timestamp.isEmpty && mappedHit.time != nil))
  }
}

@Test func canonicalStatusRejectsEveryInvalidTimestamp() {
  let invalidTimestamps = [
    "not-a-time", "2026-07-12T21:51:13", "2026-07-12T21:51:13Z trailing",
    "2026-02-30T21:51:13Z", "2026-07-12T21:51:13.Z", "2026-07-12T21:51:13.1",
  ]
  let mutations: [(inout Trawl_Federation_V1_SourceStatus, String) -> Void] = [
    { $0.generatedRfc3339 = $1 }, { $0.lastSyncRfc3339 = $1 }, { $0.lastImportRfc3339 = $1 },
    { $0.lastExportRfc3339 = $1 },
    { source, timestamp in source.remote = .with { $0.lastIngestRfc3339 = timestamp } },
    { source, timestamp in source.remote = .with { $0.lastSyncRfc3339 = timestamp } },
    { source, timestamp in source.databases = [.with { $0.modifiedRfc3339 = timestamp }] },
  ]
  for timestamp in invalidTimestamps {
    for mutate in mutations {
      var source = Trawl_Federation_V1_SourceStatus()
      source.manifest = .with {
        $0.sourceID = "synthetic"
        $0.displayName = "Synthetic"
      }
      mutate(&source, timestamp)
      var response = Trawl_Federation_V1_StatusResponse()
      response.outcome = .complete
      response.sources = [source]
      #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
    }
  }
}

@Test func canonicalSearchRejectsEveryInvalidTimestamp() {
  for timestamp in [
    "not-a-time", "2026-07-12T21:51:13", "2026-07-12T21:51:13Z trailing",
    "2026-02-30T21:51:13Z", "2026-07-12T21:51:13.Z", "2026-07-12T21:51:13.1",
  ] {
    var hit = canonicalSearchHit()
    hit.timeRfc3339 = timestamp
    var response = Trawl_Federation_V1_SearchResponse()
    response.outcome = .complete
    response.order = .recency
    response.hits = [hit]
    #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
  }
}

@Test func canonicalSearchRejectsMalformedMatchContracts() {
  let invalidHits: [Trawl_Federation_V1_SearchHit] = [
    .with {
      $0 = canonicalSearchHit()
      $0.openRef = "other:record/full"
    },
    .with {
      $0 = canonicalSearchHit()
      $0.anchorID = "matching passage"
    },
    .with {
      $0 = canonicalSearchHit()
      $0.evidence[0].label = ""
    },
  ]
  for hit in invalidHits {
    var response = Trawl_Federation_V1_SearchResponse()
    response.outcome = .complete
    response.order = .recency
    response.hits = [hit]
    #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
  }
}

@Test func canonicalSearchAcceptsUnmatchedPreviewEvidence() throws {
  var hit = canonicalSearchHit()
  hit.evidence[0].text.runs[0].matched = false
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .complete
  response.order = .recency
  response.hits = [hit]

  let mapped = try response.model().hits[0]
  #expect(
    mapped.evidence == [
      .text(label: "Matching text", runs: [.init(text: "Synthetic evidence", matched: false)])
    ])
}

@Test func canonicalSearchMapsAndValidatesArchiveContext() throws {
  var hit = canonicalSearchHit()
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .complete
  response.order = .recency
  response.hits = [hit]

  let mapped = try response.model().hits[0]
  #expect(mapped.archiveContext == [SearchArchiveContext(kind: "source", label: "In Synthetic")])

  hit = canonicalSearchHit()
  hit.archiveContext = []
  response.hits = [hit]
  #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }

  for invalidContext in [
    Trawl_Federation_V1_ArchiveContext.with {
      $0.kind = "bad kind"
      $0.label = "In Synthetic"
    },
    Trawl_Federation_V1_ArchiveContext.with {
      $0.kind = "source"
      $0.label = " "
    },
  ] {
    hit = canonicalSearchHit()
    hit.archiveContext = [invalidContext]
    response.hits = [hit]
    #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
  }
}

@Test func openRejectsDuplicateMissingAndMismatchedPrimaryAnchors() {
  var duplicate = canonicalOpenResponse()
  duplicate.record.presentation.blocks.append(
    .with {
      $0.anchorID = "match"
      $0.prose = .with { $0.text = "Duplicate target" }
    })
  #expect(throws: TrawlClientError.invalidProtobuf) { try duplicate.model() }

  var metadataDuplicate = canonicalOpenResponse()
  metadataDuplicate.record.presentation.blocks.append(
    .with {
      $0.resource = .with {
        $0.kind = .image
        $0.label = "Synthetic image"
        $0.ref = "synthetic:resource/example-1"
        $0.metadata = [
          .with {
            $0.label = "Match"
            $0.display = "Synthetic"
            $0.anchorID = "match"
          }
        ]
      }
    })
  #expect(throws: TrawlClientError.invalidProtobuf) { try metadataDuplicate.model() }

  var missing = canonicalOpenResponse()
  missing.record.presentation.blocks[0].anchorID = "other"
  #expect(throws: TrawlClientError.invalidProtobuf) { try missing.model() }

  var mismatched = canonicalOpenResponse()
  mismatched.requestedAnchorID = "other"
  #expect(throws: TrawlClientError.invalidProtobuf) { try mismatched.model() }
}

@Test func resourceMappingRejectsUnsafeShapes() throws {
  let valid = Trawl_Presentation_V1_ResourceResponse.with {
    $0.resourceRef = "photos:resource/example-1"
    $0.contentType = "image/jpeg"
    $0.data = Data("synthetic bytes".utf8)
  }
  #expect(try valid.model().data == Data("synthetic bytes".utf8))
  for invalid in [
    Trawl_Presentation_V1_ResourceResponse.with {
      $0 = valid
      $0.contentType = "image jpeg"
    },
    Trawl_Presentation_V1_ResourceResponse.with {
      $0 = valid
      $0.data = Data()
    },
  ] {
    #expect(throws: TrawlClientError.invalidProtobuf) { try invalid.model() }
  }
}

@Test func openRejectsInvalidPresenceMatrix() {
  var response = Trawl_Open_V1_OpenResponse()
  response.outcome = .complete
  #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
}

@Test func presentationMappingKeepsEveryVariant() throws {
  var document = Trawl_Presentation_V1_PresentationDocument()
  document.title = "Synthetic"
  document.primaryAnchorID = "match"
  document.blocks = [
    Trawl_Presentation_V1_Block.with {
      $0.anchorID = "match"
      $0.heading = Trawl_Presentation_V1_Heading.with { $0.text = "H" }
    },
    Trawl_Presentation_V1_Block.with {
      $0.prose = Trawl_Presentation_V1_Prose.with { $0.text = "P" }
    },
    Trawl_Presentation_V1_Block.with {
      $0.fields = Trawl_Presentation_V1_FieldGroup.with {
        $0.fields = [
          Trawl_Presentation_V1_Field.with {
            $0.label = "L"
            $0.display = "V"
          }
        ]
      }
    },
    Trawl_Presentation_V1_Block.with {
      $0.table = Trawl_Presentation_V1_Table.with {
        $0.columns = ["C"]
        $0.rows = [
          Trawl_Presentation_V1_Row.with {
            $0.role = .normal
            $0.cells = [.with { $0.display = "Normal" }]
          },
          Trawl_Presentation_V1_Row.with {
            $0.role = .target
            $0.cells = [.with { $0.display = "Target" }]
          },
        ]
      }
    },
  ]
  for kind: Trawl_Presentation_V1_Resource.Kind in [.file, .image, .video, .audio] {
    document.blocks.append(
      Trawl_Presentation_V1_Block.with {
        $0.resource = Trawl_Presentation_V1_Resource.with {
          $0.kind = kind
          $0.label = "R"
          $0.ref = "synthetic:resource/r"
        }
      })
  }
  document.actions = [
    Trawl_Presentation_V1_Action.with {
      $0.label = "R"
      $0.openRef = "synthetic:record/next"
    },
    Trawl_Presentation_V1_Action.with {
      $0.label = "U"
      $0.url = "https://example.com"
    },
  ]
  document.facts = [
    .with {
      $0.kind = .truncation
      $0.message = "T"
    },
    .with {
      $0.kind = .provenance
      $0.message = "P"
    },
    .with {
      $0.kind = .warning
      $0.message = "W"
    },
    .with {
      $0.kind = .error
      $0.message = "E"
    },
  ]
  var record = Trawl_Open_V1_OpenRecord()
  record.sourceID = "synthetic"
  record.openRef = "synthetic:record"
  record.data = Google_Protobuf_Any.with { $0.typeURL = "type.example/Synthetic" }
  record.presentation = document
  var response = Trawl_Open_V1_OpenResponse()
  response.outcome = .complete
  response.requestedAnchorID = "match"
  response.record = record
  let mapped = try response.model()
  #expect(mapped.record?.presentation.blocks.count == 8)
  #expect(mapped.record?.presentation.actions.count == 2)
  #expect(mapped.record?.presentation.facts.count == 4)
}

@Test func openRejectsEveryInvalidOutcomePresenceCombination() {
  let record = Trawl_Open_V1_OpenRecord.with {
    $0.sourceID = "synthetic"
    $0.openRef = "synthetic:record"
  }
  let invalid: [Trawl_Open_V1_OpenResponse] = [
    .with { $0.outcome = .complete },
    .with {
      $0.outcome = .complete
      $0.failure = Trawl_Federation_V1_SourceFailure()
    },
    .with { $0.outcome = .failed },
    .with { $0.outcome = .partial },
    .with { $0.outcome = .unspecified },
    .with {
      $0.outcome = .failed
      $0.record = record
    },
    .with {
      $0.outcome = .complete
      $0.record = record
      $0.failure = Trawl_Federation_V1_SourceFailure()
    },
  ]
  for response in invalid {
    #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
  }
}

@Test func searchSourceMappingPreservesTotalExactness() throws {
  var exact = Trawl_Federation_V1_SearchSourceResult()
  exact.sourceID = "exact"
  exact.displayName = "Exact"
  exact.totalMatches = 1
  exact.totalIsExact = true

  var lowerBound = Trawl_Federation_V1_SearchSourceResult()
  lowerBound.sourceID = "lower-bound"
  lowerBound.displayName = "Lower bound"
  lowerBound.totalMatches = 21

  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .complete
  response.order = .recency
  response.resultLimit = 20
  response.sources = [exact, lowerBound]
  let sources = try response.model().sources
  #expect(sources[0].totalIsExact)
  #expect(!sources[1].totalIsExact)
}

private func canonicalSearchHit() -> Trawl_Federation_V1_SearchHit {
  Trawl_Federation_V1_SearchHit.with {
    $0.sourceID = "synthetic"
    $0.openRef = "synthetic:record/full"
    $0.anchorID = "match"
    $0.summary = .with { $0.title = "Synthetic record" }
    $0.archiveContext = [
      .with {
        $0.kind = "source"
        $0.label = "In Synthetic"
      }
    ]
    $0.evidence = [
      .with {
        $0.label = "Matching text"
        $0.text = .with {
          $0.runs = [
            .with {
              $0.text = "Synthetic evidence"
              $0.matched = true
            }
          ]
        }
      }
    ]
  }
}

private func canonicalOpenResponse() -> Trawl_Open_V1_OpenResponse {
  .with {
    $0.outcome = .complete
    $0.requestedRef = "synthetic:record/full"
    $0.requestedAnchorID = "match"
    $0.record = .with {
      $0.sourceID = "synthetic"
      $0.openRef = "synthetic:record/full"
      $0.data = .with { $0.typeURL = "type.example/Synthetic" }
      $0.presentation = .with {
        $0.title = "Synthetic record"
        $0.primaryAnchorID = "match"
        $0.blocks = [
          .with {
            $0.anchorID = "match"
            $0.prose = .with { $0.text = "Synthetic matching passage" }
          }
        ]
      }
    }
  }
}
