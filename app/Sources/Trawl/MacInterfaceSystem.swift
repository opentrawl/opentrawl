import SwiftUI

enum TrawlStatus: Sendable, Equatable {
  case neutral
  case working
  case success
  case warning
  case failure

  var symbol: String {
    switch self {
    case .neutral: "circle"
    case .working: "circle"
    case .success: "checkmark.circle.fill"
    case .warning: "exclamationmark.triangle.fill"
    case .failure: "xmark.circle.fill"
    }
  }

  var colour: Color {
    switch self {
    case .neutral: .secondary
    case .working: .secondary
    case .success: .green
    case .warning: .orange
    case .failure: .red
    }
  }
}

enum OnboardingPage: Int, CaseIterable {
  case welcome
  case access
  case archive
  case commandDemo
}

enum OnboardingComposition {
  case centred
  case top
}

struct TrawlFlowScaffold<Content: View, Actions: View>: View {
  let page: OnboardingPage
  let composition: OnboardingComposition
  let contentWidth: CGFloat
  let footerWidth: CGFloat
  @ViewBuilder let content: Content
  @ViewBuilder let actions: Actions

  init(
    page: OnboardingPage,
    composition: OnboardingComposition = .top,
    contentWidth: CGFloat = TrawlDesign.onboardingPageWidth,
    footerWidth: CGFloat = TrawlDesign.onboardingPageWidth,
    @ViewBuilder content: () -> Content,
    @ViewBuilder actions: () -> Actions
  ) {
    self.page = page
    self.composition = composition
    self.contentWidth = contentWidth
    self.footerWidth = footerWidth
    self.content = content()
    self.actions = actions()
  }

  var body: some View {
    VStack(spacing: 0) {
      Group {
        if composition == .centred {
          content
            .frame(width: contentWidth)
            .frame(maxHeight: .infinity, alignment: .center)
        } else {
          content
            .frame(width: contentWidth, alignment: .topLeading)
            .padding(.top, TrawlDesign.onboardingTopInset)
            .frame(maxHeight: .infinity, alignment: .top)
        }
      }
      Divider()
        .frame(width: max(contentWidth, footerWidth))
      ZStack {
        Text(
          String(
            format: HumanCopy.SharedAction.stepProgressFormat,
            page.rawValue + 1,
            OnboardingPage.allCases.count
          )
        )
          .trawlText(.meta)
          .foregroundStyle(.tertiary)
          .accessibilityHidden(true)
        actions
          .frame(width: footerWidth)
      }
      .frame(height: TrawlDesign.onboardingFooterHeight)
    }
    .frame(
      width: TrawlDesign.onboardingWindow.width,
      height: TrawlDesign.onboardingWindow.height
    )
    .tint(TrawlDesign.brandRed)
  }
}

struct OnboardingHeroLayout<Content: View>: View {
  @ViewBuilder let content: Content

  init(@ViewBuilder content: () -> Content) {
    self.content = content()
  }

  var body: some View {
    content
      .frame(width: TrawlDesign.onboardingCopyWidth)
      .accessibilityElement(children: .contain)
  }
}

struct OnboardingTaskLayout<Heading: View, Task: View, Support: View>: View {
  @ViewBuilder let heading: Heading
  @ViewBuilder let task: Task
  @ViewBuilder let support: Support

  init(
    @ViewBuilder heading: () -> Heading,
    @ViewBuilder task: () -> Task,
    @ViewBuilder support: () -> Support
  ) {
    self.heading = heading()
    self.task = task()
    self.support = support()
  }

  var body: some View {
    VStack(alignment: .leading, spacing: 0) {
      heading
        .frame(width: TrawlDesign.onboardingReadingWidth, alignment: .topLeading)
      task
        .padding(.top, 32)
        .frame(width: TrawlDesign.onboardingPageWidth, alignment: .topLeading)
      support
        .padding(.top, TrawlDesign.onboardingBlockSpacing)
        .frame(width: TrawlDesign.onboardingReadingWidth, alignment: .topLeading)
    }
    .frame(width: TrawlDesign.onboardingPageWidth, alignment: .topLeading)
  }
}

struct OnboardingProse: View {
  let title: String
  let lede: String
  var statement: String? = nil
  var note: String? = nil
  var centred = false

  var body: some View {
    VStack(alignment: centred ? .center : .leading, spacing: 0) {
      Text(title)
        .trawlText(.pageTitle)
        .multilineTextAlignment(centred ? .center : .leading)
      Text(lede)
        .trawlText(.body)
        .lineSpacing(3)
        .foregroundStyle(.secondary)
        .multilineTextAlignment(centred ? .center : .leading)
        .fixedSize(horizontal: false, vertical: true)
        .padding(.top, TrawlDesign.onboardingIntroSpacing)
      if let statement {
        Text(statement)
          .trawlText(.body)
          .lineSpacing(3)
          .foregroundStyle(.secondary)
          .multilineTextAlignment(centred ? .center : .leading)
          .fixedSize(horizontal: false, vertical: true)
          .padding(.top, TrawlDesign.onboardingIntroSpacing)
      }
      if let note {
        Text(note)
          .trawlText(.body)
          .lineSpacing(3)
          .foregroundStyle(.secondary)
          .multilineTextAlignment(centred ? .center : .leading)
          .fixedSize(horizontal: false, vertical: true)
          .padding(.top, TrawlDesign.onboardingIntroSpacing)
      }
    }
  }
}

struct OnboardingInformationGroup<Content: View>: View {
  let title: String
  @ViewBuilder let content: Content

  init(title: String, @ViewBuilder content: () -> Content) {
    self.title = title
    self.content = content()
  }

  var body: some View {
    VStack(alignment: .leading, spacing: TrawlDesign.onboardingElementSpacing) {
      Text(title)
        .trawlText(.sectionHeader)
      content
    }
  }
}

struct OnboardingActionRow: View {
  let backAction: (() -> Void)?
  let secondaryTitle: String?
  let secondaryAction: (() -> Void)?
  let primaryTitle: String
  let primaryAction: () -> Void
  var primaryDisabled = false

  var body: some View {
    HStack(spacing: 14) {
      if let backAction {
        Button(HumanCopy.SharedAction.back, action: backAction)
          .buttonStyle(.plain)
          .foregroundStyle(.secondary)
      }
      Spacer()
      if let secondaryTitle, let secondaryAction {
        Button(secondaryTitle, action: secondaryAction)
          .buttonStyle(.plain)
          .foregroundStyle(.secondary)
      }
      Button(primaryTitle, action: primaryAction)
        .buttonStyle(.borderedProminent)
        .buttonBorderShape(.capsule)
        .disabled(primaryDisabled)
        .opacity(primaryDisabled ? 0.42 : 1)
        .keyboardShortcut(.defaultAction)
    }
  }
}

struct TrawlStatusRow: View {
  let name: String
  let counts: String?
  let detail: String?
  let status: TrawlStatus
  let statusLabel: String
  let recoveryTitle: String?
  let recovery: (() -> Void)?
  var recoveryDisabled = false

  var body: some View {
    HStack(alignment: .center, spacing: 12) {
      HStack(alignment: .center, spacing: 12) {
        Image(systemName: status.symbol)
          .foregroundStyle(status.colour)
          .accessibilityHidden(true)
        VStack(alignment: .leading, spacing: 3) {
          HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(name)
              .trawlText(.body)
              .lineLimit(1)
            if let counts, !counts.isEmpty {
              Text(counts)
                .trawlText(.meta)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            }
          }
          if let detail, !detail.isEmpty {
            Text(detail)
              .trawlText(.meta)
              .foregroundStyle(.secondary)
              .lineLimit(1)
          }
        }
        Spacer(minLength: 12)
        Text(statusLabel)
          .trawlText(.meta)
          .foregroundStyle(status.colour)
      }
      .accessibilityElement(children: .combine)
      .accessibilityLabel(accessibilitySummary)
      if let recoveryTitle, let recovery {
        Button(recoveryTitle, action: recovery)
          .controlSize(.small)
          .disabled(recoveryDisabled)
          .accessibilityLabel("\(recoveryTitle) \(name)")
      }
    }
    .padding(.horizontal, 16)
    .padding(.vertical, 6)
    .frame(minHeight: 48)
  }

  private var accessibilitySummary: String {
    [name, statusLabel, counts, detail].compactMap { $0 }.filter { !$0.isEmpty }
      .joined(separator: ", ")
  }
}
