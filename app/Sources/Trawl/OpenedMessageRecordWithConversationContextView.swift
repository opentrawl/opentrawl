import SwiftUI
import TrawlClient

enum OpenedMessageTimeline {
  /// Converts the wire-order newest-first conversation window into natural
  /// chronological reading order (oldest at the top, newest at the bottom).
  static func chronologicalMessages(
    from messagesNewestFirst: [MessageRecord]
  ) -> [MessageRecord] {
    Array(messagesNewestFirst.reversed())
  }
}

struct OpenedMessageRecordWithConversationContextView: View {
  let openedMessage: OpenedMessageRecordWithConversationContext
  let recordTrawler: RegisteredTrawlerIdentity
  let trawlerDisplayName: String?
  let targetAnchor: RecordAnchorIdentifier
  let trawlLink: GloballyRoutableTrawlLink
  let helperController: TrawlHelperController?
  var onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)?

  @State private var isShowingAround = false
  @State private var earlierMessages: [ExpandedConversationMessageLine] = []
  @State private var laterMessages: [ExpandedConversationMessageLine] = []
  @State private var isLoadingEarlier = false
  @State private var isLoadingLater = false
  @State private var didExhaustEarlier = false
  @State private var didExhaustLater = false

  private var chronologicalMessages: [MessageRecord] {
    OpenedMessageTimeline.chronologicalMessages(
      from: openedMessage.conversationContextMessageRecordsNewestFirst
    )
  }

  private var participantsSubtitle: String? {
    let names = openedMessage.conversationParticipantDisplayNames
    guard !names.isEmpty else { return nil }
    if names.count == 1,
      names.first?.localizedCaseInsensitiveCompare(openedMessage.conversationDisplayName)
        == .orderedSame
    {
      return nil
    }
    return names.formatted(.list(type: .and))
  }

  private var oldestMessageDate: Date? {
    chronologicalMessages.first?.messageTime.flatMap(exactOrCalendarDate)
  }

  private var newestMessageDate: Date? {
    chronologicalMessages.last?.messageTime.flatMap(exactOrCalendarDate)
  }

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          RecordDetailHeader(
            registeredTrawler: recordTrawler,
            trawlerDisplayName: trawlerDisplayName,
            title: openedMessage.conversationDisplayName,
            subtitle: participantsSubtitle,
            trawlLink: trawlLink,
            showsAroundToggle: helperController != nil,
            isShowingAround: isShowingAround,
            onToggleAround: { isShowingAround.toggle() }
          )

          if isShowingAround, let helperController {
            AroundRelatedRecordsSection(
              trawlLink: trawlLink,
              helperController: helperController,
              onOpenLink: onOpenLink
            )
          }

          Divider()

          if openedMessage.earlierConversationContextMessagesOmitted {
            ConversationContextExpansionControl(
              label: OperationalCopy.Record.loadEarlierMessages,
              systemImage: "arrow.up.circle",
              isLoading: isLoadingEarlier,
              didExhaust: didExhaustEarlier,
              canLoad: helperController != nil && !openedMessage.conversationTrawlLink.globallyRoutableTrawlLink.isEmpty,
              onLoad: loadEarlierMessages
            )
          }

          if !earlierMessages.isEmpty {
            ExpandedConversationMessageList(messages: earlierMessages)
          }

          OpenedMessageConversationContext(
            messages: chronologicalMessages,
            openedMessageRecordReference: openedMessage.openedMessageRecordReference,
            targetAnchor: targetAnchor
          )

          if let mediaForOpenedMessage {
            OpenedMessageMediaDetails(messageMedia: mediaForOpenedMessage)
          }

          if !laterMessages.isEmpty {
            ExpandedConversationMessageList(messages: laterMessages)
          }

          if openedMessage.laterConversationContextMessagesOmitted {
            ConversationContextExpansionControl(
              label: OperationalCopy.Record.loadLaterMessages,
              systemImage: "arrow.down.circle",
              isLoading: isLoadingLater,
              didExhaust: didExhaustLater,
              canLoad: helperController != nil && !openedMessage.conversationTrawlLink.globallyRoutableTrawlLink.isEmpty,
              onLoad: loadLaterMessages
            )
          }
        }
        .padding(20)
        .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
        .frame(maxWidth: .infinity, alignment: .leading)
      }
      .onAppear {
        scrollToTarget(using: proxy)
      }
      .onChange(of: openedMessage.openedMessageRecordReference) { _, _ in
        earlierMessages = []
        laterMessages = []
        didExhaustEarlier = false
        didExhaustLater = false
        isShowingAround = false
        scrollToTarget(using: proxy)
      }
    }
  }

  private func scrollToTarget(using proxy: ScrollViewProxy) {
    guard !targetAnchor.recordAnchorIdentifier.isEmpty else { return }
    DispatchQueue.main.async {
      proxy.scrollTo(targetAnchor.recordAnchorIdentifier, anchor: .center)
    }
  }

  private var mediaForOpenedMessage: MessageMedia? {
    chronologicalMessages.first {
      $0.canonicalRecordReference == openedMessage.openedMessageRecordReference
    }?.messageMedia
  }

  private func exactOrCalendarDate(_ associated: ArchiveRecordAssociatedTimeForDisplay) -> Date {
    switch associated {
    case .exactTime(let date), .calendarDate(let date):
      date
    }
  }

  private func loadEarlierMessages() {
    guard let helperController, !isLoadingEarlier else { return }
    isLoadingEarlier = true
    let link = openedMessage.conversationTrawlLink
    let beforeDate = oldestMessageDate
    Task {
      let fetched = await helperController.loadConversationMessages(
        conversationLink: link,
        before: beforeDate,
        limit: 20
      )
      // Reverse to oldest-first if CLI returned newest-first, deduplicating existing lines.
      let existingTexts = Set(chronologicalMessages.map(\.messageTextAndMediaDescription))
      let filtered = fetched.reversed().filter { !existingTexts.contains($0.messageText) }
      if filtered.isEmpty {
        didExhaustEarlier = true
      } else {
        earlierMessages = filtered
      }
      isLoadingEarlier = false
    }
  }

  private func loadLaterMessages() {
    guard let helperController, !isLoadingLater else { return }
    isLoadingLater = true
    let link = openedMessage.conversationTrawlLink
    let afterDate = newestMessageDate
    Task {
      let fetched = await helperController.loadConversationMessages(
        conversationLink: link,
        after: afterDate,
        limit: 20
      )
      let existingTexts = Set(chronologicalMessages.map(\.messageTextAndMediaDescription))
      let filtered = fetched.reversed().filter { !existingTexts.contains($0.messageText) }
      if filtered.isEmpty {
        didExhaustLater = true
      } else {
        laterMessages = filtered
      }
      isLoadingLater = false
    }
  }
}

private struct ConversationContextExpansionControl: View {
  let label: String
  let systemImage: String
  let isLoading: Bool
  let didExhaust: Bool
  let canLoad: Bool
  let onLoad: () -> Void

  var body: some View {
    HStack {
      Spacer()
      if isLoading {
        HStack(spacing: 6) {
          ProgressView()
            .controlSize(.mini)
          Text(OperationalCopy.Record.loadingMoreMessages)
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .padding(.vertical, 4)
      } else if didExhaust {
        Text(OperationalCopy.Record.noAdditionalMessages)
          .font(.caption)
          .foregroundStyle(.tertiary)
          .padding(.vertical, 4)
      } else if canLoad {
        Button(action: onLoad) {
          Label(label, systemImage: systemImage)
            .font(.caption.weight(.medium))
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
      }
      Spacer()
    }
  }
}

private struct ExpandedConversationMessageList: View {
  let messages: [ExpandedConversationMessageLine]

  var body: some View {
    LazyVStack(alignment: .leading, spacing: 8) {
      ForEach(messages) { message in
        VStack(alignment: .leading, spacing: 4) {
          HStack(alignment: .firstTextBaseline, spacing: 8) {
            if !message.senderText.isEmpty {
              Text(message.senderText)
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            }
            Spacer(minLength: 8)
            Text(message.timestampText)
              .font(.caption2.monospacedDigit())
              .foregroundStyle(.tertiary)
          }
          Text(message.messageText)
            .font(.callout)
            .foregroundStyle(.primary)
            .textSelection(.enabled)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
          TrawlDesign.surfaceBackground.opacity(0.75),
          in: RoundedRectangle(cornerRadius: 10, style: .continuous)
        )
        .overlay(
          RoundedRectangle(cornerRadius: 10, style: .continuous)
            .strokeBorder(TrawlDesign.hairlineBorder.opacity(0.6), lineWidth: 0.5)
        )
      }
    }
  }
}

private struct OpenedMessageConversationContext: View {
  let messages: [MessageRecord]
  let openedMessageRecordReference: CanonicalArchiveRecordReference
  let targetAnchor: RecordAnchorIdentifier

  var body: some View {
    LazyVStack(alignment: .leading, spacing: 10) {
      ForEach(Array(messages.enumerated()), id: \.element.id) { index, message in
        if let dayHeader = daySeparator(at: index) {
          ConversationDayDivider(date: dayHeader)
        }
        let isOpenedMessage =
          message.canonicalRecordReference == openedMessageRecordReference
        OpenedMessageConversationRow(
          messageTime: message.messageTime,
          senderDisplayNames: senderDisplayNames(for: message),
          messageTextAndMediaDescription: message.messageTextAndMediaDescription,
          isOpenedMessage: isOpenedMessage
        )
        .id(
          isOpenedMessage
            ? targetAnchor.recordAnchorIdentifier
            : message.canonicalRecordReference.canonicalArchiveRecordReference
        )
      }
    }
  }

  private func daySeparator(at index: Int) -> Date? {
    guard let currentDate = dateValue(for: messages[index].messageTime) else { return nil }
    if index == 0 {
      return currentDate
    }
    guard let previousDate = dateValue(for: messages[index - 1].messageTime) else {
      return currentDate
    }
    return Calendar.current.isDate(currentDate, inSameDayAs: previousDate) ? nil : currentDate
  }

  private func dateValue(for time: ArchiveRecordAssociatedTimeForDisplay?) -> Date? {
    switch time {
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

private struct ConversationDayDivider: View {
  let date: Date

  var body: some View {
    HStack(spacing: 10) {
      Rectangle()
        .fill(TrawlDesign.hairlineBorder.opacity(0.7))
        .frame(height: 0.5)
      Text(date, format: .dateTime.day().month(.abbreviated).year())
        .font(.caption2.weight(.medium))
        .foregroundStyle(.tertiary)
        .fixedSize()
      Rectangle()
        .fill(TrawlDesign.hairlineBorder.opacity(0.7))
        .frame(height: 0.5)
    }
    .padding(.vertical, 4)
  }
}

private struct OpenedMessageConversationRow: View {
  let messageTime: ArchiveRecordAssociatedTimeForDisplay?
  let senderDisplayNames: [String]
  let messageTextAndMediaDescription: String
  let isOpenedMessage: Bool

  private var isFromSelf: Bool {
    senderDisplayNames.count == 1
      && senderDisplayNames.first?.localizedCaseInsensitiveCompare("me") == .orderedSame
  }

  private var formattedSender: String {
    if isFromSelf { return "You" }
    return senderDisplayNames.formatted()
  }

  var body: some View {
    HStack(spacing: 0) {
      if isOpenedMessage {
        RoundedRectangle(cornerRadius: 2)
          .fill(TrawlDesign.brandRed)
          .frame(width: 3)
          .padding(.vertical, 6)
          .padding(.leading, 4)
      }
      VStack(alignment: .leading, spacing: 5) {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
          if !formattedSender.isEmpty {
            Text(formattedSender)
              .font(.caption.weight(.semibold))
              .foregroundStyle(isFromSelf ? TrawlDesign.brandRed : .primary)
          }
          if isOpenedMessage {
            Text(OperationalCopy.Record.matchedBadge)
              .font(.caption2.weight(.semibold))
              .foregroundStyle(TrawlDesign.brandRed)
              .padding(.horizontal, 6)
              .padding(.vertical, 1.5)
              .background(TrawlDesign.brandRed.opacity(0.14), in: Capsule())
          }
          Spacer(minLength: 8)
          OpenedMessageTime(messageTime: messageTime)
        }
        Text(messageTextAndMediaDescription)
          .font(.body)
          .lineSpacing(3)
          .foregroundStyle(.primary)
          .textSelection(.enabled)
      }
      .padding(.horizontal, 12)
      .padding(.vertical, 10)
      .frame(maxWidth: .infinity, alignment: .leading)
    }
    .background(
      isOpenedMessage
        ? TrawlDesign.brandRed.opacity(0.09)
        : (isFromSelf ? TrawlDesign.surfaceBackground.opacity(0.65) : TrawlDesign.surfaceBackground),
      in: RoundedRectangle(cornerRadius: 10, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: 10, style: .continuous)
        .strokeBorder(
          isOpenedMessage ? TrawlDesign.brandRed.opacity(0.32) : TrawlDesign.hairlineBorder.opacity(0.65),
          lineWidth: isOpenedMessage ? 1 : 0.5
        )
    )
    .accessibilityElement(children: .combine)
  }
}

private struct OpenedMessageTime: View {
  let messageTime: ArchiveRecordAssociatedTimeForDisplay?

  var body: some View {
    switch messageTime {
    case .exactTime(let time):
      Text(time, format: .dateTime.hour().minute())
        .font(.caption.monospacedDigit())
        .foregroundStyle(.tertiary)
    case .calendarDate(let date):
      Text(date, format: .dateTime.year().month().day())
        .font(.caption.monospacedDigit())
        .foregroundStyle(.tertiary)
    case nil:
      EmptyView()
    }
  }
}

private struct OpenedMessageMediaDetails: View {
  let messageMedia: MessageMedia

  var body: some View {
    RecordMetadataCard {
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
    .font(.callout)
  }
}
