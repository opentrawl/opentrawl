import AppKit
import SwiftUI
import TrawlClient

struct OpenedMessageRecordWithConversationContextView: View {
  let openedMessage: OpenedMessageRecordWithConversationContext
  let targetAnchor: RecordAnchorIdentifier
  var highlightedTerms: [String] = []

  private var messagesOldestFirst: [MessageRecord] {
    Array(openedMessage.conversationContextMessageRecordsNewestFirst.reversed())
  }

  var body: some View {
    VStack(spacing: 0) {
      OpenedMessageConversationHeader(
        conversationDisplayName: openedMessage.conversationDisplayName,
        participantDisplayNames: openedMessage.conversationParticipantDisplayNames,
        conversationTrawlLink: openedMessage.conversationTrawlLink
      )
      .padding(.horizontal, 18)
      .padding(.vertical, 12)
      .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
      .frame(maxWidth: .infinity, alignment: .leading)

      Divider()

      conversationContext
    }
  }

  private var conversationContext: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          if openedMessage.earlierConversationContextMessagesOmitted {
            OpenedMessageBoundaryNotice(
              text: "Earlier messages are not shown.")
          }
          OpenedMessageConversationContext(
            messagesOldestFirst: messagesOldestFirst,
            openedMessageRecordReference: openedMessage.openedMessageRecordReference,
            targetAnchor: targetAnchor,
            highlightedTerms: highlightedTerms)
          if let mediaForOpenedMessage {
            OpenedMessageMediaDetails(messageMedia: mediaForOpenedMessage)
          }
          if openedMessage.laterConversationContextMessagesOmitted {
            OpenedMessageBoundaryNotice(
              text: "Later messages are not shown.")
          }
        }
        .padding(18)
        .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
        .frame(maxWidth: .infinity, alignment: .leading)
      }
      .onAppear {
        scrollToTarget(using: proxy)
      }
      .onChange(of: openedMessage.openedMessageRecordReference) { _, _ in
        scrollToTarget(using: proxy)
      }
    }
  }

  private func scrollToTarget(using proxy: ScrollViewProxy) {
    guard !targetAnchor.recordAnchorIdentifier.isEmpty else { return }
    proxy.scrollTo(targetAnchor.recordAnchorIdentifier, anchor: .center)
  }

  private var mediaForOpenedMessage: MessageMedia? {
    openedMessage.conversationContextMessageRecordsNewestFirst.first {
      $0.canonicalRecordReference == openedMessage.openedMessageRecordReference
    }?.messageMedia
  }
}

private struct OpenedMessageConversationHeader: View {
  let conversationDisplayName: String
  let participantDisplayNames: [String]
  let conversationTrawlLink: GloballyRoutableTrawlLink

  @State private var copiedLink = false

  var body: some View {
    VStack(alignment: .leading, spacing: 6) {
      HStack(alignment: .firstTextBaseline, spacing: 12) {
        Text(conversationDisplayName)
          .font(TrawlDesign.Typography.pageTitle)
          .textSelection(.enabled)
        Spacer(minLength: 8)
        if !conversationTrawlLink.globallyRoutableTrawlLink.isEmpty {
          Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(
              conversationTrawlLink.globallyRoutableTrawlLink,
              forType: .string)
            copiedLink = true
            Task { @MainActor in
              try? await Task.sleep(for: .seconds(2))
              copiedLink = false
            }
          } label: {
            Label(
              copiedLink ? "Copied" : "Conversation link",
              systemImage: copiedLink ? "checkmark" : "link"
            )
          }
          .buttonStyle(.bordered)
          .controlSize(.small)
          .help("Copy a link your AI can open to read this conversation")
        }
      }
      if !participantDisplayNames.isEmpty
        && (participantDisplayNames.count != 1
          || participantDisplayNames.first?.localizedCaseInsensitiveCompare(
            conversationDisplayName) != .orderedSame)
      {
        Text(participantDisplayNames.formatted())
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
          .textSelection(.enabled)
      }
    }
  }
}

private struct OpenedMessageConversationContext: View {
  let messagesOldestFirst: [MessageRecord]
  let openedMessageRecordReference: CanonicalArchiveRecordReference
  let targetAnchor: RecordAnchorIdentifier
  let highlightedTerms: [String]

  var body: some View {
    LazyVStack(alignment: .leading, spacing: 12) {
      ForEach(Array(messagesOldestFirst.enumerated()), id: \.element.id) { index, message in
        if shouldShowDayHeader(at: index), let date = messageDate(message.messageTime) {
          OpenedMessageDayDivider(date: date)
            .padding(.top, index == 0 ? 0 : 6)
        }
        let isOpenedMessage =
          message.canonicalRecordReference == openedMessageRecordReference
        OpenedMessageConversationRow(
          messageTime: message.messageTime,
          senderDisplayNames: senderDisplayNames(for: message),
          messageTextAndMediaDescription: message.messageTextAndMediaDescription,
          isOpenedMessage: isOpenedMessage,
          highlightedTerms: isOpenedMessage ? highlightedTerms : []
        )
        .id(
          isOpenedMessage
            ? targetAnchor.recordAnchorIdentifier
            : message.canonicalRecordReference.canonicalArchiveRecordReference)
      }
    }
  }

  private func shouldShowDayHeader(at index: Int) -> Bool {
    guard let currentDate = messageDate(messagesOldestFirst[index].messageTime) else {
      return false
    }
    guard index > 0, let previousDate = messageDate(messagesOldestFirst[index - 1].messageTime)
    else {
      return true
    }
    return !Calendar.current.isDate(currentDate, inSameDayAs: previousDate)
  }

  private func messageDate(_ associatedTime: ArchiveRecordAssociatedTimeForDisplay?) -> Date? {
    switch associatedTime {
    case .exactTime(let date), .calendarDate(let date):
      date
    case nil:
      nil
    }
  }

  private func senderDisplayNames(for message: MessageRecord) -> [String] {
    message.peopleRelatedToMessage.compactMap { person in
      switch person.personRoleInArchiveRecord {
      case .sender, .author:
        person.personDisplayName
      case .recipient, .organizer, .attendee, .participant, nil:
        nil
      }
    }
  }
}

private struct OpenedMessageDayDivider: View {
  let date: Date

  var body: some View {
    HStack(spacing: 10) {
      Rectangle()
        .fill(.quaternary)
        .frame(height: 1)
      Text(date, format: .dateTime.year().month(.abbreviated).day())
        .font(TrawlDesign.Typography.smallLabel.weight(.medium))
        .foregroundStyle(.secondary)
        .fixedSize()
      Rectangle()
        .fill(.quaternary)
        .frame(height: 1)
    }
  }
}

private struct OpenedMessageConversationRow: View {
  let messageTime: ArchiveRecordAssociatedTimeForDisplay?
  let senderDisplayNames: [String]
  let messageTextAndMediaDescription: String
  let isOpenedMessage: Bool
  let highlightedTerms: [String]

  private static let bubbleMaximumWidth: CGFloat = 540

  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
      HStack(alignment: .firstTextBaseline, spacing: 6) {
        if !senderDisplayNames.isEmpty {
          Text(senderDisplayNames.formatted())
            .font(TrawlDesign.Typography.meta.weight(.semibold))
            .foregroundStyle(.primary)
        }
        OpenedMessageTime(messageTime: messageTime)
        if isOpenedMessage {
          Text("· Match")
            .font(TrawlDesign.Typography.smallLabel.weight(.semibold))
            .foregroundStyle(TrawlDesign.Colors.matchRed)
        }
      }
      .padding(.leading, 12)

      HighlightedMessageText.styledText(
        for: messageTextAndMediaDescription,
        highlighting: highlightedTerms
      )
        .font(TrawlDesign.Typography.body)
        .lineSpacing(2)
        .textSelection(.enabled)
        .fixedSize(horizontal: false, vertical: true)
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .background(
          isOpenedMessage
            ? TrawlDesign.Colors.matchRed.opacity(0.06) : TrawlDesign.Colors.messageBubble,
          in: TrawlDesign.Shape.surface
        )
        .overlay {
          if isOpenedMessage {
            TrawlDesign.Shape.surface
              .strokeBorder(TrawlDesign.Colors.matchRed, lineWidth: 1.25)
          }
        }
    }
    .frame(maxWidth: Self.bubbleMaximumWidth, alignment: .leading)
    .frame(maxWidth: .infinity, alignment: .leading)
  }
}

enum HighlightedMessageText {
  struct Segment: Equatable {
    let text: String
    let isMatch: Bool
  }

  /// Splits message text into matched and unmatched segments for highlighting
  /// search terms inside an opened bubble, preferring longer terms first so
  /// overlapping matches stay whole.
  static func segments(in text: String, highlighting rawTerms: [String]) -> [Segment] {
    guard !text.isEmpty else { return [] }
    let terms = orderedDistinctTerms(from: rawTerms)
    guard !terms.isEmpty else {
      return [Segment(text: text, isMatch: false)]
    }

    var matches: [Range<String.Index>] = []
    var cursor = text.startIndex
    while cursor < text.endIndex {
      var bestRange: Range<String.Index>?
      for term in terms {
        guard
          let found = text.range(
            of: term,
            options: [.caseInsensitive, .diacriticInsensitive],
            range: cursor..<text.endIndex
          )
        else { continue }
        if let existing = bestRange {
          if found.lowerBound < existing.lowerBound
            || (found.lowerBound == existing.lowerBound && found.upperBound > existing.upperBound)
          {
            bestRange = found
          }
        } else {
          bestRange = found
        }
      }
      guard let matchRange = bestRange else { break }
      matches.append(matchRange)
      cursor = matchRange.upperBound
    }

    guard !matches.isEmpty else {
      return [Segment(text: text, isMatch: false)]
    }

    var result: [Segment] = []
    var position = text.startIndex
    for matchRange in matches {
      if position < matchRange.lowerBound {
        result.append(Segment(text: String(text[position..<matchRange.lowerBound]), isMatch: false))
      }
      result.append(Segment(text: String(text[matchRange]), isMatch: true))
      position = matchRange.upperBound
    }
    if position < text.endIndex {
      result.append(Segment(text: String(text[position..<text.endIndex]), isMatch: false))
    }
    return result
  }

  static func styledText(
    for text: String,
    highlighting terms: [String],
    highlightColor: Color = TrawlDesign.Colors.matchRed,
    underlineMatch: Bool = false
  ) -> Text {
    let pieces = segments(in: text, highlighting: terms)
    guard !pieces.isEmpty else { return Text(text) }
    var combined = Text("")
    for piece in pieces {
      var pieceText = Text(piece.text)
      if piece.isMatch {
        pieceText = pieceText.bold().foregroundColor(highlightColor)
        if underlineMatch {
          pieceText = pieceText.underline()
        }
      }
      combined = Text("\(combined)\(pieceText)")
    }
    return combined
  }

  private static func orderedDistinctTerms(from rawTerms: [String]) -> [String] {
    var seen = Set<String>()
    var unique: [String] = []
    for term in rawTerms {
      let trimmed = term.trimmingCharacters(in: .whitespacesAndNewlines)
      let key = trimmed.folding(options: [.caseInsensitive, .diacriticInsensitive], locale: .current)
      guard !trimmed.isEmpty, seen.insert(key).inserted else { continue }
      unique.append(trimmed)
    }
    return unique.sorted { $0.count > $1.count }
  }
}

private struct OpenedMessageTime: View {
  let messageTime: ArchiveRecordAssociatedTimeForDisplay?

  var body: some View {
    Group {
      switch messageTime {
      case .exactTime(let time):
        Text(time, format: .dateTime.hour().minute())
      case .calendarDate(let date):
        Text(date, format: .dateTime.year().month().day())
      case nil:
        EmptyView()
      }
    }
    .font(TrawlDesign.Typography.smallNumber)
    .foregroundStyle(.secondary)
  }
}

private struct OpenedMessageMediaDetails: View {
  let messageMedia: MessageMedia

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      if !messageMedia.messageMediaTitle.isEmpty {
        LabeledContent("Media", value: messageMedia.messageMediaTitle)
      } else if let messageMediaContentKind = messageMedia.messageMediaContentKind {
        LabeledContent(
          "Media",
          value: messageMediaContentKind.messageMediaContentKindDisplayName)
      }
      if let messageMediaByteCount = messageMedia.messageMediaByteCount {
        LabeledContent(
          "Size",
          value: ByteCountFormatter.string(
            fromByteCount: Int64(messageMediaByteCount),
            countStyle: .file))
      }
      if let messageMediaHTTPSURL = messageMedia.messageMediaHTTPSURL {
        Link("Open media", destination: messageMediaHTTPSURL)
      }
      if let messageMediaMetadataHTTPSURL = messageMedia.messageMediaMetadataHTTPSURL {
        Link("Open media details", destination: messageMediaMetadataHTTPSURL)
      }
    }
    .font(TrawlDesign.Typography.meta)
  }
}

private struct OpenedMessageBoundaryNotice: View {
  let text: String

  var body: some View {
    Text(text)
      .font(TrawlDesign.Typography.smallLabel)
      .foregroundStyle(.tertiary)
      .frame(maxWidth: .infinity, alignment: .center)
      .padding(.vertical, 2)
  }
}
