import Testing

@testable import Trawl
@testable import TrawlClient

@Test func emptyGenericResourcesDoNotCreateRecordCards() {
  let emptyVideo = PresentationResource(
    kind: .video,
    label: "Resource",
    ref: "synthetic:resource/empty-video",
    metadata: [],
    anchorID: "empty-video"
  )
  let namedFile = PresentationResource(
    kind: .file,
    label: "Synthetic agenda",
    ref: "synthetic:resource/agenda",
    metadata: [],
    anchorID: "agenda"
  )
  let image = PresentationResource(
    kind: .image,
    label: "Resource",
    ref: "synthetic:resource/image",
    metadata: [],
    anchorID: "image"
  )

  #expect(!PresentationResourceVisibility.isVisible(emptyVideo))
  #expect(PresentationResourceVisibility.isVisible(namedFile))
  #expect(PresentationResourceVisibility.isVisible(image))
}
