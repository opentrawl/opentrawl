import Foundation
import SwiftUI

/// Formats plain body text (emails, notes, calendar descriptions, and chat
/// messages) into an `AttributedString` with real clickable links and optional
/// highlighted search terms.
///
/// Recognises:
/// - Markdown links: `[Label](https://...)`
/// - HTML-to-text extracted link pairs: `Help (https://...)`, `X (Twitter) (https://...)`
/// - Bare `http://` and `https://` URLs (shortening long tracking URLs visually
///   while preserving the full destination URL on the link)
enum RichBodyText {
  private static let maxDisplayedBareURLLength = 64
  private static let maxPrecedingLabelCharacters = 40
  private static let maxPrecedingLabelWords = 4

  struct LinkReplacement {
    let replaceRange: Range<String.Index>
    let displayText: String
    let url: URL
  }

  static func attributedString(
    for rawText: String,
    highlighting terms: [String] = [],
    highlightColor: Color = TrawlDesign.Colors.matchRed
  ) -> AttributedString {
    guard !rawText.isEmpty else { return AttributedString() }

    let replacements = linkReplacements(in: rawText)
    var rendered = AttributedString()
    var cursor = rawText.startIndex

    for replacement in replacements {
      if cursor < replacement.replaceRange.lowerBound {
        let plainSegment = String(rawText[cursor..<replacement.replaceRange.lowerBound])
        rendered.append(highlightedSegment(plainSegment, terms: terms, highlightColor: highlightColor))
      }
      var linkSegment = highlightedSegment(
        replacement.displayText,
        terms: terms,
        highlightColor: highlightColor,
        defaultColor: TrawlDesign.Colors.selectBlue
      )
      linkSegment.link = replacement.url
      linkSegment.underlineStyle = .single
      rendered.append(linkSegment)
      cursor = replacement.replaceRange.upperBound
    }

    if cursor < rawText.endIndex {
      let trailingSegment = String(rawText[cursor..<rawText.endIndex])
      rendered.append(highlightedSegment(trailingSegment, terms: terms, highlightColor: highlightColor))
    }

    return rendered
  }

  static func linkReplacements(in text: String) -> [LinkReplacement] {
    var result: [LinkReplacement] = []
    var claimedUpperBound = text.startIndex

    // 1. Markdown links: [Label](https://...)
    let markdownPattern = /\[([^\]\n]{1,120})\]\((https?:\/\/[^\s\)]+)\)/
    var markdownMatches: [(range: Range<String.Index>, label: String, url: URL)] = []
    for match in text.matches(of: markdownPattern) {
      let label = String(match.output.1).trimmingCharacters(in: .whitespacesAndNewlines)
      let urlString = String(match.output.2)
      guard !label.isEmpty, let url = URL(string: urlString) else { continue }
      markdownMatches.append((match.range, label, url))
    }

    // 2. All http(s) URLs in text, skipping any already covered by markdown links.
    let urlPattern = /https?:\/\/[^\s<>\]\)\"\']+/
    var candidates: [LinkReplacement] = markdownMatches.map {
      LinkReplacement(replaceRange: $0.range, displayText: $0.label, url: $0.url)
    }

    for match in text.matches(of: urlPattern) {
      let rawRange = match.range
      if markdownMatches.contains(where: { $0.range.overlaps(rawRange) }) {
        continue
      }
      let trimmedRange = trimTrailingSentencePunctuation(in: text, range: rawRange)
      let urlString = String(text[trimmedRange])
      guard let url = URL(string: urlString) else { continue }

      // Check for HTML-to-text pair: "Label (https://...)"
      if let paired = parenthesizedLabelReplacement(in: text, urlRange: trimmedRange, url: url) {
        candidates.append(paired)
      } else {
        candidates.append(
          LinkReplacement(
            replaceRange: trimmedRange,
            displayText: displayText(forBareURL: url, rawString: urlString),
            url: url
          )
        )
      }
    }

    candidates.sort { lhs, rhs in
      if lhs.replaceRange.lowerBound != rhs.replaceRange.lowerBound {
        return lhs.replaceRange.lowerBound < rhs.replaceRange.lowerBound
      }
      return lhs.replaceRange.upperBound > rhs.replaceRange.upperBound
    }

    for candidate in candidates {
      guard candidate.replaceRange.lowerBound >= claimedUpperBound else { continue }
      result.append(candidate)
      claimedUpperBound = candidate.replaceRange.upperBound
    }

    return result
  }

  /// Recognises `Label (https://...)` when `Label` is a short link label on the
  /// same line immediately before the parenthesized URL, such as `Help (https://...)`
  /// or `X (Twitter) (https://...)`.
  private static func parenthesizedLabelReplacement(
    in text: String,
    urlRange: Range<String.Index>,
    url: URL
  ) -> LinkReplacement? {
    guard urlRange.lowerBound > text.startIndex,
      urlRange.upperBound < text.endIndex
    else {
      return nil
    }
    let openParenIndex = text.index(before: urlRange.lowerBound)
    guard text[openParenIndex] == "(", text[urlRange.upperBound] == ")" else {
      return nil
    }
    let closingEndIndex = text.index(after: urlRange.upperBound)

    // Require at least one space between the label and "(".
    var labelEnd = openParenIndex
    while labelEnd > text.startIndex {
      let prev = text.index(before: labelEnd)
      if text[prev] == " " || text[prev] == "\t" {
        labelEnd = prev
      } else {
        break
      }
    }
    guard labelEnd < openParenIndex else { return nil }

    // Walk backward to the start of the label on the same line, stopping at
    // common inline separators (`·`, `•`, `|`, `,`, `;`, `:`, em/en-dash, or newlines).
    var labelStart = labelEnd
    var consecutiveSpaces = 0
    while labelStart > text.startIndex {
      let prev = text.index(before: labelStart)
      let ch = text[prev]
      if ch.isNewline || ch == "·" || ch == "•" || ch == "|" || ch == "," || ch == ";"
        || ch == ":" || ch == "—" || ch == "–"
      {
        break
      }
      if ch == " " || ch == "\t" {
        consecutiveSpaces += 1
        if consecutiveSpaces >= 2 {
          labelStart = text.index(after: labelStart)
          break
        }
      } else {
        consecutiveSpaces = 0
      }
      labelStart = prev
    }

    // Trim leading whitespace from the label.
    while labelStart < labelEnd, text[labelStart] == " " || text[labelStart] == "\t" {
      labelStart = text.index(after: labelStart)
    }
    guard labelStart < labelEnd else { return nil }

    let label = String(text[labelStart..<labelEnd])
    guard isPlausibleLinkLabel(label) else { return nil }

    return LinkReplacement(
      replaceRange: labelStart..<closingEndIndex,
      displayText: label,
      url: url
    )
  }

  private static func isPlausibleLinkLabel(_ label: String) -> Bool {
    guard !label.isEmpty, label.count <= maxPrecedingLabelCharacters else { return false }
    if let last = label.last, ".!?".contains(last) {
      return false
    }
    let words = label.split(whereSeparator: \.isWhitespace)
    guard !words.isEmpty, words.count <= maxPrecedingLabelWords else { return false }
    guard label.contains(where: { $0.isLetter || $0.isNumber }) else { return false }
    return true
  }

  private static func trimTrailingSentencePunctuation(
    in text: String,
    range: Range<String.Index>
  ) -> Range<String.Index> {
    var end = range.upperBound
    while end > range.lowerBound {
      let prev = text.index(before: end)
      let ch = text[prev]
      if ".,;:!?".contains(ch) {
        end = prev
      } else {
        break
      }
    }
    return range.lowerBound..<end
  }

  private static func displayText(forBareURL url: URL, rawString: String) -> String {
    guard rawString.count > maxDisplayedBareURLLength else {
      return rawString
    }
    guard let host = url.host, !host.isEmpty else {
      return String(rawString.prefix(maxDisplayedBareURLLength - 1)) + "…"
    }
    let cleanPath = url.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    if cleanPath.isEmpty {
      return "\(host)/…"
    }
    let shortPath = cleanPath.count > 24 ? String(cleanPath.prefix(24)) : cleanPath
    return "\(host)/\(shortPath)…"
  }

  private static func highlightedSegment(
    _ text: String,
    terms: [String],
    highlightColor: Color,
    defaultColor: Color? = nil
  ) -> AttributedString {
    let pieces = HighlightedMessageText.segments(in: text, highlighting: terms)
    var result = AttributedString()
    for piece in pieces {
      var part = AttributedString(piece.text)
      if piece.isMatch {
        part.inlinePresentationIntent = .stronglyEmphasized
        part.foregroundColor = highlightColor
      } else if let defaultColor {
        part.foregroundColor = defaultColor
      }
      result.append(part)
    }
    return result
  }
}
