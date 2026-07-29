import Foundation

struct GloballyRoutableTrawlLinkRoute {
  let registeredTrawlerManifestIdentity: String
  let localShortReferenceAcceptedByRegisteredTrawler: String
}

private let globallyRoutableTrawlLinkLocalShortReferenceAlphabet =
  Set("23456789abcdefghjkmnpqrstuvwxyz")

func parseGloballyRoutableTrawlLink(
  _ globallyRoutableTrawlLink: String
) -> GloballyRoutableTrawlLinkRoute? {
  let trimmedGloballyRoutableTrawlLink = globallyRoutableTrawlLink.trimmingCharacters(
    in: .whitespacesAndNewlines)
  let linkComponents = trimmedGloballyRoutableTrawlLink.split(
    separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
  guard linkComponents.count == 2 else { return nil }

  let registeredTrawlerManifestIdentity = linkComponents[0].trimmingCharacters(
    in: .whitespacesAndNewlines)
  let localShortReferenceAcceptedByRegisteredTrawler = linkComponents[1].trimmingCharacters(
    in: .whitespacesAndNewlines)
  guard
    !registeredTrawlerManifestIdentity.isEmpty,
    (5...52).contains(localShortReferenceAcceptedByRegisteredTrawler.utf8.count),
    localShortReferenceAcceptedByRegisteredTrawler.allSatisfy(
      globallyRoutableTrawlLinkLocalShortReferenceAlphabet.contains)
  else { return nil }

  let canonicalGloballyRoutableTrawlLink =
    "\(registeredTrawlerManifestIdentity):\(localShortReferenceAcceptedByRegisteredTrawler)"
  guard canonicalGloballyRoutableTrawlLink == trimmedGloballyRoutableTrawlLink else { return nil }

  return GloballyRoutableTrawlLinkRoute(
    registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
    localShortReferenceAcceptedByRegisteredTrawler:
      localShortReferenceAcceptedByRegisteredTrawler)
}
