import SwiftUI
import TrawlClient

struct OpenedMessageRecordWithConversationContextView: View {
  let openedMessage: OpenedMessageRecordWithConversationContext
  let targetAnchor: RecordAnchorIdentifier

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          OpenedMessageConversationHeader(
            conversationDisplayName: openedMessage.conversationDisplayName,
            participantDisplayNames: openedMessage.conversationParticipantDisplayNames,
            conversationTrawlLink: openedMessage.conversationTrawlLink)
          OpenedMessageConversationContext(
            messages: openedMessage.conversationContextMessageRecordsInDisplayOrder,
            openedMessageRecordReference: openedMessage.openedMessageRecordReference,
            targetAnchor: targetAnchor)
          if let openedMessageMedia = openedMessage.openedMessageMedia {
            OpenedMessageMediaDetails(messageMedia: openedMessageMedia)
          }
          OpenedMessageOmissionNotice(
            earlierMessagesOmitted: openedMessage.earlierConversationContextMessagesOmitted,
            laterMessagesOmitted: openedMessage.laterConversationContextMessagesOmitted)
        }
        .padding(18)
        .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
        .frame(maxWidth: .infinity, alignment: .leading)
      }
      .onAppear {
        guard !targetAnchor.recordAnchorIdentifier.isEmpty else { return }
        proxy.scrollTo(targetAnchor.recordAnchorIdentifier, anchor: .center)
      }
    }
  }
}

private struct OpenedMessageConversationHeader: View {
  let conversationDisplayName: String
  let participantDisplayNames: [String]
  let conversationTrawlLink: GloballyRoutableTrawlLink

  var body: some View {
    VStack(alignment: .leading, spacing: 6) {
      Text(conversationDisplayName)
        .font(.title2)
        .textSelection(.enabled)
      if !participantDisplayNames.isEmpty
        && (participantDisplayNames.count != 1
          || participantDisplayNames.first?.localizedCaseInsensitiveCompare(
            conversationDisplayName) != .orderedSame)
      {
        Text(
          "Participants: \(participantDisplayNames.formatted())",
          bundle: #bundle,
          comment: "People in the opened message conversation.")
          .foregroundStyle(.secondary)
          .textSelection(.enabled)
      }
      if !conversationTrawlLink.globallyRoutableTrawlLink.isEmpty {
        LabeledContent("Link", value: conversationTrawlLink.globallyRoutableTrawlLink)
          .font(.caption)
          .foregroundStyle(.secondary)
      }
    }
  }
}

private struct OpenedMessageConversationContext: View {
  let messages: [MessageRecord]
  let openedMessageRecordReference: CanonicalArchiveRecordReference
  let targetAnchor: RecordAnchorIdentifier

  var body: some View {
    LazyVStack(alignment: .leading, spacing: 8) {
      ForEach(messages) { message in
        let isOpenedMessage =
          message.canonicalRecordReference == openedMessageRecordReference
        OpenedMessageConversationRow(
          messageTime: message.messageTime,
          senderDisplayNames: senderDisplayNames(for: message),
          displayedMessageOrMediaText: message.displayedMessageOrMediaText,
          isOpenedMessage: isOpenedMessage)
          .id(
            isOpenedMessage
              ? targetAnchor.recordAnchorIdentifier
              : message.canonicalRecordReference.canonicalArchiveRecordReference)
      }
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

private struct OpenedMessageConversationRow: View {
  let messageTime: ArchiveRecordAssociatedTimeForDisplay?
  let senderDisplayNames: [String]
  let displayedMessageOrMediaText: String
  let isOpenedMessage: Bool

  var body: some View {
    VStack(alignment: .leading, spacing: 6) {
      HStack(alignment: .firstTextBaseline, spacing: 8) {
        if isOpenedMessage {
          Image(systemName: "arrow.right")
            .accessibilityLabel(
              Text(
                "Opened message", bundle: #bundle,
                comment: "Accessibility label for the selected message."))
        }
        OpenedMessageTime(messageTime: messageTime)
        Spacer(minLength: 0)
      }
      if !senderDisplayNames.isEmpty {
        Text(
          "From: \(senderDisplayNames.formatted())",
          bundle: #bundle,
          comment: "People who sent the message.")
          .font(.callout)
          .foregroundStyle(.secondary)
      }
      Text(displayedMessageOrMediaText)
        .textSelection(.enabled)
    }
    .padding(10)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(
      isOpenedMessage ? Color.accentColor.opacity(0.12) : .secondary.opacity(0.05),
      in: .rect(cornerRadius: 8))
  }
}

private struct OpenedMessageTime: View {
  let messageTime: ArchiveRecordAssociatedTimeForDisplay?

  var body: some View {
    HStack {
      switch messageTime {
      case .exactTime(let time):
        Text(time, format: .dateTime.year().month().day().hour().minute())
      case .calendarDate(let date):
        Text(date, format: .dateTime.year().month().day())
      case nil:
        EmptyView()
      }
    }
    .font(.caption)
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
        LabeledContent("Media", value: messageMediaContentKind.displayName)
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

extension MessageMediaContentKind {
  fileprivate var displayName: String {
    switch self {
    case .image: "Image"
    case .video: "Video"
    case .audio: "Audio"
    case .file: "File"
    }
  }
}

private struct OpenedMessageOmissionNotice: View {
  let earlierMessagesOmitted: Bool
  let laterMessagesOmitted: Bool

  var body: some View {
    VStack(alignment: .leading) {
      if earlierMessagesOmitted && laterMessagesOmitted {
        Text(
          "Earlier and later messages are not shown.", bundle: #bundle,
          comment: "Notice below a partial conversation around an opened message.")
      } else if earlierMessagesOmitted {
        Text(
          "Earlier messages are not shown.", bundle: #bundle,
          comment: "Notice below a partial conversation around an opened message.")
      } else if laterMessagesOmitted {
        Text(
          "Later messages are not shown.", bundle: #bundle,
          comment: "Notice below a partial conversation around an opened message.")
      }
    }
    .font(.callout)
    .foregroundStyle(.secondary)
  }
}
