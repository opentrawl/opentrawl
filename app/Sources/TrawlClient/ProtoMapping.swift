import Foundation

extension Trawl_App_V1_SourceStatus {
  var model: SourceStatus {
    SourceStatus(
      id: appID,
      name: surface,
      state: state,
      summary: summary,
      counts: counts.map { SourceCount(id: $0.id, display: $0.display) },
      lastSyncedDisplay: lastSyncedDisplay,
      archiveBytes: archiveBytes
    )
  }
}

extension Trawl_App_V1_SearchHit {
  var model: SearchHit {
    SearchHit(
      id: openRef,
      sourceID: appID,
      title: title,
      snippet: snippet,
      whenDisplay: whenDisplay
    )
  }
}
