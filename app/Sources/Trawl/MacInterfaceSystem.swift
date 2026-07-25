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
    case .working: "arrow.trianglehead.2.clockwise.rotate.90"
    case .success: "checkmark.circle.fill"
    case .warning: "exclamationmark.triangle.fill"
    case .failure: "xmark.circle.fill"
    }
  }

  var colour: Color {
    switch self {
    case .neutral: .secondary
    case .working: TrawlDesign.brandRed
    case .success: .green
    case .warning: .orange
    case .failure: .red
    }
  }
}

struct TrawlFlowScaffold<Content: View, Footer: View>: View {
  let step: String
  @ViewBuilder let content: Content
  @ViewBuilder let footer: Footer

  init(
    step: String,
    @ViewBuilder content: () -> Content,
    @ViewBuilder footer: () -> Footer
  ) {
    self.step = step
    self.content = content()
    self.footer = footer()
  }

  var body: some View {
    VStack(spacing: 0) {
      TrawlBrandRail(step: step)
      Rectangle().frame(height: 2)
      ScrollView {
        content
          .frame(maxWidth: TrawlDesign.flowReadingWidth, alignment: .leading)
          .padding(.horizontal, TrawlDesign.flowContentInset)
          .padding(.vertical, TrawlDesign.flowContentInset)
          .frame(maxWidth: .infinity, alignment: .top)
      }
      Divider()
      footer
        .padding(.horizontal, TrawlDesign.flowActionInset)
        .frame(minHeight: TrawlDesign.flowActionHeight)
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
    .tint(TrawlDesign.brandRed)
    .buttonBorderShape(.roundedRectangle(radius: 4))
  }
}

struct TrawlActionBar: View {
  let backAction: (() -> Void)?
  let secondaryTitle: String?
  let secondaryAction: (() -> Void)?
  let primaryTitle: String
  let primaryAction: () -> Void
  var primaryDisabled = false

  @FocusState private var primaryFocused: Bool

  var body: some View {
    actions
      .task { primaryFocused = true }
  }

  private var actions: some View {
    HStack(spacing: 10) {
      if let backAction {
        Button(OperationalCopy.back, action: backAction)
          .keyboardShortcut(.cancelAction)
      }
      Spacer(minLength: 12)
      if let secondaryTitle, let secondaryAction {
        Button(secondaryTitle, action: secondaryAction)
      }
      Button(primaryTitle, action: primaryAction)
        .buttonStyle(.borderedProminent)
        .keyboardShortcut(.defaultAction)
        .focused($primaryFocused)
        .disabled(primaryDisabled)
    }
    .controlSize(.large)
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
            Text(name).font(.headline)
            if let counts, !counts.isEmpty {
              Text(counts).foregroundStyle(.secondary)
            }
          }
          if let detail, !detail.isEmpty {
            Text(detail)
              .font(.callout)
              .foregroundStyle(.secondary)
          }
        }
        Spacer(minLength: 12)
        Text(statusLabel)
          .foregroundStyle(status.colour)
      }
      .accessibilityElement(children: .combine)
      .accessibilityLabel(accessibilitySummary)
      if let recoveryTitle, let recovery {
        Button(recoveryTitle, action: recovery)
          .disabled(recoveryDisabled)
          .accessibilityLabel("\(recoveryTitle) \(name)")
      }
    }
    .padding(.horizontal, 16)
    .padding(.vertical, 10)
    .frame(minHeight: 56)
  }

  private var accessibilitySummary: String {
    [name, statusLabel, counts, detail].compactMap { $0 }.filter { !$0.isEmpty }
      .joined(separator: ", ")
  }
}

private struct TrawlBrandRail: View {
  let step: String

  var body: some View {
    HStack(alignment: .firstTextBaseline) {
      HStack(spacing: 0) {
        Text("open").foregroundStyle(.primary)
        Text("trawl").foregroundStyle(TrawlDesign.brandRed)
      }
      .font(.body.bold())
      .tracking(-0.2)
      Spacer()
      Text(step)
        .font(.caption.bold())
        .tracking(1.1)
        .foregroundStyle(TrawlDesign.brandRed)
    }
    .padding(.horizontal, TrawlDesign.flowActionInset)
    .frame(height: TrawlDesign.flowBrandRailHeight)
  }
}
