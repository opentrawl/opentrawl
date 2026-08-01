import Foundation

struct GloballyRoutableTrawlLinkRoute {
  let registeredTrawler: RegisteredTrawlerIdentity
  let localShortReference: LocalTrawlerShortReference
}

private let globallyRoutableTrawlLinkLocalShortReferenceAlphabet =
  Set("23456789abcdefghjkmnpqrstuvwxyz")

func parseGloballyRoutableTrawlLink(
  _ globallyRoutableTrawlLink: GloballyRoutableTrawlLink
) -> GloballyRoutableTrawlLinkRoute? {
  let trimmedGloballyRoutableTrawlLink =
    globallyRoutableTrawlLink.globallyRoutableTrawlLink.trimmingCharacters(
    in: .whitespacesAndNewlines)
  let linkComponents = trimmedGloballyRoutableTrawlLink.split(
    separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
  guard linkComponents.count == 2 else { return nil }

  let registeredTrawlerIdentity = linkComponents[0].trimmingCharacters(
    in: .whitespacesAndNewlines)
  let localShortReferenceAcceptedByRegisteredTrawler = linkComponents[1].trimmingCharacters(
    in: .whitespacesAndNewlines)
  guard
    !registeredTrawlerIdentity.isEmpty,
    (5...52).contains(localShortReferenceAcceptedByRegisteredTrawler.utf8.count),
    localShortReferenceAcceptedByRegisteredTrawler.allSatisfy(
      globallyRoutableTrawlLinkLocalShortReferenceAlphabet.contains)
  else { return nil }

  let canonicalGloballyRoutableTrawlLink =
    "\(registeredTrawlerIdentity):\(localShortReferenceAcceptedByRegisteredTrawler)"
  guard canonicalGloballyRoutableTrawlLink == trimmedGloballyRoutableTrawlLink else { return nil }

  return GloballyRoutableTrawlLinkRoute(
    registeredTrawler: RegisteredTrawlerIdentity(
      registeredTrawlerIdentity: registeredTrawlerIdentity),
    localShortReference: LocalTrawlerShortReference(
      localTrawlerShortReference: localShortReferenceAcceptedByRegisteredTrawler))
}
