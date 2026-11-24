import SwiftUI
import TrawlClient
import TrawlCore

private enum ConstellationAnchorID: Hashable {
  case source(RegisteredTrawlerIdentity)
  case hub
  case destination(ConstellationDestinationKind)
}

private enum ConstellationDestinationKind: Hashable {
  case localAI
  case cloudAI
  case otherMacs
}

private struct ConstellationAnchorPreferenceKey: PreferenceKey {
  static let defaultValue: [ConstellationAnchorID: Anchor<CGRect>] = [:]

  static func reduce(
    value: inout [ConstellationAnchorID: Anchor<CGRect>],
    nextValue: () -> [ConstellationAnchorID: Anchor<CGRect>]
  ) {
    value.merge(nextValue(), uniquingKeysWith: { _, latest in latest })
  }
}

/// Overview (Home) view:
/// 1. Top: "Your archive" borderless constellation connecting source apps -> This Mac hub ->
///    AI & Other Macs using anchor-measured coloured Bezier curves and real arrowheads.
/// 2. Bottom: "Items per week" inside a native GroupBox using the sqrt-recency time axis
///    (`ArchiveTimelineTimeAxis`) and power-law vertical intensity (`ArchiveTimelineIntensityScale`).
struct ArchiveOverviewView: View {
  @Environment(TrawlerIconStore.self) private var iconStore

  let trawlers: [RestingTrawler]
  let statusesByID: [RegisteredTrawlerIdentity: TrawlerStatus]
  let operabilityByID: [RegisteredTrawlerIdentity: AppOperabilityState]
  let hostDisplayName: String
  let freshnessLine: String
  let isRemoteArchive: Bool
  let hasCopiedAIPrompt: Bool
  let onCopyAIPrompt: () -> Void
  let onOpenAIAppsSettings: () -> Void
  let onOpenOtherMacsSettings: () -> Void
  let onOpenHostSwitcher: () -> Void
  let onRunAttentionAction: (RegisteredTrawlerIdentity, AppAttentionAction) -> Void
  let onBrowseApp: (RegisteredTrawlerIdentity) -> Void
  let onBrowseAllItems: () -> Void
  let onSelectDateRange: (RegisteredTrawlerIdentity?, Date, Date) -> Void

  @State private var chartSpan: OverviewChartSpan = .all
  @State private var hoveredWeek: HoveredTimelineWeek?
  @State private var dragRange: TimelineDragSelection?

  private var datedTrawlers: [RestingTrawler] {
    trawlers.filter { $0.id.registeredTrawlerIdentity != "contacts" }
  }

  private var hasUndatedContacts: Bool {
    trawlers.contains { $0.id.registeredTrawlerIdentity == "contacts" }
  }

  private var timeAxis: ArchiveTimelineTimeAxis {
    let now = Date()
    let calendar = Calendar(identifier: .gregorian)
    switch chartSpan {
    case .oneYear:
      let start = calendar.date(byAdding: .year, value: -1, to: now) ?? now.addingTimeInterval(-365 * 86_400)
      return ArchiveTimelineTimeAxis(referenceDate: now, earliestDatedRecordTime: start)
    case .fiveYears:
      let start = calendar.date(byAdding: .year, value: -5, to: now) ?? now.addingTimeInterval(-5 * 365 * 86_400)
      return ArchiveTimelineTimeAxis(referenceDate: now, earliestDatedRecordTime: start)
    case .all:
      let histories = datedTrawlers.compactMap { statusesByID[$0.id]?.archiveWeeklyHistory }
      return ArchiveTimelineTimeAxis.build(from: histories, referenceDate: now)
    }
  }

  var body: some View {
    GeometryReader { outerProxy in
      let isHorizontalFlow = outerProxy.size.width >= Self.narrowestHorizontalFlowWidth

      ScrollView {
        VStack(alignment: .leading, spacing: TrawlDesign.Space.lg) {
          constellationSection(isHorizontalFlow: isHorizontalFlow)
          weeklyLinesSection
        }
        .padding(TrawlDesign.Space.lg)
        .frame(width: outerProxy.size.width, alignment: .topLeading)
      }
    }
  }

  // MARK: - Top: Borderless Constellation Flow with Anchored Coloured Arrows

  private static let narrowestHorizontalFlowWidth: CGFloat = 1_080
  private static let sourceAppChipWidth: CGFloat = 178
  private static let sourceAppChipHeight: CGFloat = 30
  private static let sourceAppChipSpacing: CGFloat = 12
  private static let archiveHubWidth: CGFloat = 216
  private static let destinationCardWidth: CGFloat = 328

  private func constellationSection(isHorizontalFlow: Bool) -> some View {
    GroupBox {
      VStack(alignment: .leading, spacing: TrawlDesign.Space.md) {
        Text("Your archive")
          .font(TrawlDesign.Typography.itemTitle)
          .foregroundStyle(.primary)

        Group {
          if isHorizontalFlow {
            horizontalConstellation
          } else {
            verticalConstellation
          }
        }
        .backgroundPreferenceValue(ConstellationAnchorPreferenceKey.self) { anchors in
          GeometryReader { proxy in
            Canvas { context, _ in
              drawConstellationArrows(
                in: &context,
                anchors: anchors,
                proxy: proxy,
                isHorizontalFlow: isHorizontalFlow
              )
            }
          }
        }
      }
      .padding(.horizontal, 12)
      .padding(.top, 10)
      .padding(.bottom, 14)
    }
  }

  /// Apps alternate between two columns, and the second column sits half a row
  /// lower. Read top to bottom, the nodes are then in app order, and the curve
  /// of a first-column app runs through the gap between two second-column apps.
  private var sourceAppColumns: some View {
    let firstColumn = trawlers.enumerated().filter { $0.offset.isMultiple(of: 2) }.map(\.element)
    let secondColumn = trawlers.enumerated().filter { !$0.offset.isMultiple(of: 2) }.map(\.element)
    return HStack(alignment: .top, spacing: TrawlDesign.Space.md) {
      VStack(spacing: Self.sourceAppChipSpacing) {
        ForEach(firstColumn) { sourceAppChip(for: $0) }
      }
      VStack(spacing: Self.sourceAppChipSpacing) {
        ForEach(secondColumn) { sourceAppChip(for: $0) }
      }
      .padding(.top, (Self.sourceAppChipHeight + Self.sourceAppChipSpacing) / 2)
    }
  }

  private var horizontalConstellation: some View {
    HStack(alignment: .center, spacing: 0) {
      sourceAppColumns

      Spacer(minLength: 44)

      archiveHubNode
        .frame(width: Self.archiveHubWidth)
        .anchorPreference(key: ConstellationAnchorPreferenceKey.self, value: .bounds) {
          [.hub: $0]
        }

      Spacer(minLength: 44)

      VStack(alignment: .leading, spacing: 10) {
        if !isRemoteArchive {
          destinationCard(
            kind: .localAI,
            symbol: "terminal",
            title: "AI apps on this Mac",
            subtitle: "Claude Code, Codex, Cursor and others",
            buttonTitle: hasCopiedAIPrompt ? "Copied" : HumanCopy.ConnectAI.copyAction,
            isProminent: true,
            action: onCopyAIPrompt
          )
          destinationCard(
            kind: .cloudAI,
            symbol: "globe",
            title: "AI on web or phone",
            subtitle: "Let Claude, ChatGPT or Grok search this archive",
            buttonTitle: "Set up…",
            isProminent: false,
            action: onOpenAIAppsSettings
          )
        }
        destinationCard(
          kind: .otherMacs,
          symbol: "laptopcomputer.and.iphone",
          title: "Other Macs",
          subtitle: isRemoteArchive
            ? "Reading archive on \(hostDisplayName)"
            : "Search this archive from another Mac",
          buttonTitle: "Manage…",
          isProminent: false,
          action: onOpenOtherMacsSettings
        )
      }
      .frame(width: Self.destinationCardWidth)
    }
    .frame(maxWidth: .infinity)
  }

  private var verticalConstellation: some View {
    VStack(alignment: .center, spacing: 36) {
      ViewThatFits(in: .horizontal) {
        compactSourceRow(iconSize: 24, spacing: 10)
        compactSourceRow(iconSize: 20, spacing: 4)
        compactSourceRow(iconSize: 16, spacing: 2)
      }

      archiveHubNode
        .frame(minWidth: 220, maxWidth: 244)
        .anchorPreference(key: ConstellationAnchorPreferenceKey.self, value: .bounds) {
          [.hub: $0]
        }

      HStack(alignment: .top, spacing: TrawlDesign.Space.md) {
        if !isRemoteArchive {
          compactDestinationColumn(
            kind: .localAI,
            symbol: "terminal",
            title: "AI apps on this Mac",
            subtitle: "Claude Code, Codex, Cursor",
            buttonTitle: hasCopiedAIPrompt ? "Copied" : HumanCopy.ConnectAI.copyAction,
            isProminent: true,
            action: onCopyAIPrompt
          )
          compactDestinationColumn(
            kind: .cloudAI,
            symbol: "globe",
            title: "AI on web or phone",
            subtitle: "Claude, ChatGPT or Grok",
            buttonTitle: "Set up…",
            isProminent: false,
            action: onOpenAIAppsSettings
          )
        }
        compactDestinationColumn(
          kind: .otherMacs,
          symbol: "laptopcomputer.and.iphone",
          title: "Other Macs",
          subtitle: isRemoteArchive ? hostDisplayName : "Search from another Mac",
          buttonTitle: "Manage…",
          isProminent: false,
          action: onOpenOtherMacsSettings
        )
      }
      .frame(maxWidth: .infinity)
    }
    .frame(maxWidth: .infinity)
  }

  private func sourceAppChip(for trawler: RestingTrawler) -> some View {
    let op = operabilityByID[trawler.id]
    let needsAttention = op?.needsAttention == true
    let archivedItemCount = op?.archivedItemCount ?? 0
    let inlineActionTitle: String? = {
      guard needsAttention, let action = op?.action else { return nil }
      switch action {
      case .signInGmail, .signInX: return "Sign in"
      case .openSourceApp: return "Open"
      case .openFullDiskAccessSettings, .openPrivacySettings, .retryUpdate: return nil
      }
    }()

    return HStack(spacing: 6) {
      Button {
        onBrowseApp(trawler.id)
      } label: {
        HStack(spacing: 6) {
          TrawlerIconView(registeredTrawler: trawler.id, size: 16)
          Text(trawler.registeredTrawlerDisplayName)
            .font(TrawlDesign.Typography.meta.weight(.medium))
            .foregroundStyle(.primary)
            .lineLimit(1)
          if needsAttention && inlineActionTitle == nil {
            Circle()
              .fill(TrawlDesign.Colors.warningAmber)
              .frame(width: 6, height: 6)
          }
          Spacer(minLength: 4)
          if inlineActionTitle == nil, archivedItemCount > 0 {
            Text(archivedItemCount.formatted())
              .font(TrawlDesign.Typography.smallNumber)
              .foregroundStyle(.secondary)
          }
        }
        .frame(maxHeight: .infinity)
        .contentShape(Rectangle())
      }
      .buttonStyle(.plain)

      if let inlineActionTitle, let action = op?.action {
        Button(inlineActionTitle) {
          onRunAttentionAction(trawler.id, action)
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .tint(TrawlDesign.Colors.warningAmberText)
      }
    }
    .padding(.horizontal, 9)
    .frame(width: Self.sourceAppChipWidth, height: Self.sourceAppChipHeight)
    .background(TrawlDesign.Colors.surface, in: TrawlDesign.Shape.control)
    .overlay(
      TrawlDesign.Shape.control
        .strokeBorder(
          needsAttention
            ? TrawlDesign.Colors.warningAmber.opacity(0.55) : TrawlDesign.Colors.hairline,
          lineWidth: 1)
    )
    .help(op?.statusLine ?? trawler.registeredTrawlerDisplayName)
    .anchorPreference(key: ConstellationAnchorPreferenceKey.self, value: .bounds) {
      [.source(trawler.id): $0]
    }
  }

  private func compactSourceRow(iconSize: CGFloat, spacing: CGFloat) -> some View {
    HStack(spacing: spacing) {
      ForEach(trawlers) { trawler in
        compactSourceChip(for: trawler, iconSize: iconSize)
          .anchorPreference(key: ConstellationAnchorPreferenceKey.self, value: .bounds) {
            [.source(trawler.id): $0]
          }
      }
    }
  }

  private func compactSourceChip(for trawler: RestingTrawler, iconSize: CGFloat) -> some View {
    let op = operabilityByID[trawler.id]
    return Button {
      onBrowseApp(trawler.id)
    } label: {
      ZStack(alignment: .topTrailing) {
        TrawlerIconView(registeredTrawler: trawler.id, size: iconSize)
        if op?.needsAttention == true {
          Circle()
            .fill(TrawlDesign.Colors.warningAmber)
            .frame(width: 7, height: 7)
            .offset(x: 3, y: -2)
        }
      }
      .frame(width: iconSize + 6, height: iconSize + 6)
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .help("\(trawler.registeredTrawlerDisplayName) · \(op?.statusLine ?? "")")
    .accessibilityLabel(trawler.registeredTrawlerDisplayName)
  }

  private var archivedItemCountOfAllApps: UInt64 {
    trawlers.reduce(UInt64(0)) { $0 + (operabilityByID[$1.id]?.archivedItemCount ?? 0) }
  }

  private var archiveHubNode: some View {
    Button(action: onOpenHostSwitcher) {
      VStack(spacing: 5) {
        OpenTrawlMarkIcon(size: 40)
          .padding(.bottom, 3)
        HStack(spacing: 4) {
          Text(hostDisplayName)
            .font(TrawlDesign.Typography.itemTitle)
            .foregroundStyle(.primary)
            .lineLimit(1)
          Image(systemName: "chevron.up.chevron.down")
            .font(TrawlDesign.Typography.smallLabel)
            .foregroundStyle(.secondary)
        }
        if archivedItemCountOfAllApps > 0 {
          Text("\(archivedItemCountOfAllApps.formatted()) items")
            .font(TrawlDesign.Typography.monoSmall.weight(.medium))
            .foregroundStyle(.primary)
        }
        Text(freshnessLine)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
          .lineLimit(1)
      }
      .frame(maxWidth: .infinity)
      .padding(.horizontal, 14)
      .padding(.vertical, 16)
      .background(TrawlDesign.Colors.surface, in: TrawlDesign.Shape.surface)
      .overlay(
        TrawlDesign.Shape.surface
          .strokeBorder(TrawlDesign.Colors.hairline, lineWidth: 1)
      )
      .shadow(color: .black.opacity(0.07), radius: 10, y: 3)
    }
    .buttonStyle(.plain)
    .help("Switch archive host")
  }

  private func destinationCard(
    kind: ConstellationDestinationKind,
    symbol: String,
    title: String,
    subtitle: String,
    buttonTitle: String,
    isProminent: Bool,
    action: @escaping () -> Void
  ) -> some View {
    HStack(alignment: .center, spacing: 12) {
      Image(systemName: symbol)
        .font(TrawlDesign.Typography.symbolEmphasis)
        .foregroundStyle(isProminent ? Color.accentColor : .secondary)
        .frame(width: 24, alignment: .center)

      VStack(alignment: .leading, spacing: 2) {
        Text(title)
          .font(TrawlDesign.Typography.bodyEmphasis)
          .foregroundStyle(.primary)
          .lineLimit(1)
        Text(subtitle)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
          .fixedSize(horizontal: false, vertical: true)
      }

      Spacer(minLength: 8)

      if isProminent {
        Button(buttonTitle, action: action)
          .buttonStyle(.borderedProminent)
          .controlSize(.small)
      } else {
        Button(buttonTitle, action: action)
          .buttonStyle(.bordered)
          .controlSize(.small)
      }
    }
    .padding(12)
    .background(TrawlDesign.Colors.surface, in: TrawlDesign.Shape.surface)
    .overlay(
      TrawlDesign.Shape.surface
        .strokeBorder(
          isProminent
            ? TrawlDesign.Colors.selectBlue.opacity(0.40) : TrawlDesign.Colors.hairline,
          lineWidth: 1)
    )
    .anchorPreference(key: ConstellationAnchorPreferenceKey.self, value: .bounds) {
      [.destination(kind): $0]
    }
  }

  private func compactDestinationColumn(
    kind: ConstellationDestinationKind,
    symbol: String,
    title: String,
    subtitle: String,
    buttonTitle: String,
    isProminent: Bool,
    action: @escaping () -> Void
  ) -> some View {
    VStack(alignment: .center, spacing: 6) {
      Image(systemName: symbol)
        .font(TrawlDesign.Typography.symbolEmphasis)
        .foregroundStyle(isProminent ? Color.accentColor : .secondary)
      Text(title)
        .font(TrawlDesign.Typography.bodyEmphasis)
        .foregroundStyle(.primary)
        .multilineTextAlignment(.center)
        .lineLimit(1)
      Text(subtitle)
        .font(TrawlDesign.Typography.meta)
        .foregroundStyle(.secondary)
        .multilineTextAlignment(.center)
        .lineLimit(2)
      if isProminent {
        Button(buttonTitle, action: action)
          .buttonStyle(.borderedProminent)
          .controlSize(.small)
      } else {
        Button(buttonTitle, action: action)
          .buttonStyle(.bordered)
          .controlSize(.small)
      }
    }
    .frame(maxWidth: .infinity)
    .padding(.top, 6)
    .anchorPreference(key: ConstellationAnchorPreferenceKey.self, value: .bounds) {
      [.destination(kind): $0]
    }
  }

  private func drawConstellationArrows(
    in context: inout GraphicsContext,
    anchors: [ConstellationAnchorID: Anchor<CGRect>],
    proxy: GeometryProxy,
    isHorizontalFlow: Bool
  ) {
    guard let hubAnchor = anchors[.hub] else { return }
    let hubRect = proxy[hubAnchor]

    let destinationKinds: [ConstellationDestinationKind] =
      isRemoteArchive ? [.otherMacs] : [.localAI, .cloudAI, .otherMacs]

    if isHorizontalFlow {
      drawHubRings(in: &context, around: hubRect)

      let sourceRects = trawlers.compactMap { trawler in
        anchors[.source(trawler.id)].map { (trawler: trawler, rect: proxy[$0]) }
      }
      let secondColumnTrailingEdge = sourceRects.map(\.rect.maxX).max() ?? 0
      let sourceCount = max(1, sourceRects.count)
      for (index, source) in sourceRects.enumerated() {
        let nodeEdge = CGPoint(x: source.rect.maxX, y: source.rect.midY)
        let curveStart = CGPoint(x: secondColumnTrailingEdge + 4, y: source.rect.midY)
        let hubYFraction =
          sourceCount == 1 ? 0.5 : (0.12 + 0.76 * Double(index) / Double(sourceCount - 1))
        let end = CGPoint(
          x: hubRect.minX - 5,
          y: hubRect.minY + hubRect.height * CGFloat(hubYFraction)
        )
        let color = iconStore.laneAccentColor(for: source.trawler.id).opacity(0.70)
        if nodeEdge.x < curveStart.x - 4 {
          var lineThroughSecondColumnGap = Path()
          lineThroughSecondColumnGap.move(to: nodeEdge)
          lineThroughSecondColumnGap.addLine(to: curveStart)
          context.stroke(
            lineThroughSecondColumnGap,
            with: .color(color),
            style: StrokeStyle(lineWidth: 1.5, lineCap: .round))
        }
        drawCurvedArrow(
          in: &context,
          from: curveStart,
          to: end,
          horizontal: true,
          color: color,
          lineWidth: 1.5
        )
      }

      let destCount = max(1, destinationKinds.count)
      for (index, kind) in destinationKinds.enumerated() {
        guard let destAnchor = anchors[.destination(kind)] else { continue }
        let dstRect = proxy[destAnchor]
        let hubYFraction = destCount == 1 ? 0.5 : (0.25 + 0.50 * Double(index) / Double(destCount - 1))
        let start = CGPoint(
          x: hubRect.maxX + 4,
          y: hubRect.minY + hubRect.height * CGFloat(hubYFraction)
        )
        let end = CGPoint(x: dstRect.minX - 5, y: dstRect.midY)
        let color: Color =
          kind == .localAI
          ? Color.accentColor.opacity(0.75)
          : Color(nsColor: .secondaryLabelColor).opacity(0.50)
        drawCurvedArrow(
          in: &context,
          from: start,
          to: end,
          horizontal: true,
          color: color,
          lineWidth: kind == .localAI ? 1.8 : 1.4
        )
      }
    } else {
      let sourceCount = max(1, trawlers.count)
      for (index, trawler) in trawlers.enumerated() {
        guard let sourceAnchor = anchors[.source(trawler.id)] else { continue }
        let srcRect = proxy[sourceAnchor]
        let start = CGPoint(x: srcRect.midX, y: srcRect.maxY + 4)
        let hubXFraction = sourceCount == 1 ? 0.5 : (0.15 + 0.70 * Double(index) / Double(sourceCount - 1))
        let end = CGPoint(
          x: hubRect.minX + hubRect.width * CGFloat(hubXFraction),
          y: hubRect.minY - 6
        )
        let color = iconStore.laneAccentColor(for: trawler.id).opacity(0.55)
        drawCurvedArrow(
          in: &context,
          from: start,
          to: end,
          horizontal: false,
          color: color,
          lineWidth: 1.4
        )
      }

      let destCount = max(1, destinationKinds.count)
      for (index, kind) in destinationKinds.enumerated() {
        guard let destAnchor = anchors[.destination(kind)] else { continue }
        let dstRect = proxy[destAnchor]
        let hubXFraction = destCount == 1 ? 0.5 : (0.22 + 0.56 * Double(index) / Double(destCount - 1))
        let start = CGPoint(
          x: hubRect.minX + hubRect.width * CGFloat(hubXFraction),
          y: hubRect.maxY + 4
        )
        let end = CGPoint(x: dstRect.midX, y: dstRect.minY - 6)
        let color: Color =
          kind == .localAI
          ? Color.accentColor.opacity(0.70)
          : Color(nsColor: .secondaryLabelColor).opacity(0.45)
        drawCurvedArrow(
          in: &context,
          from: start,
          to: end,
          horizontal: false,
          color: color,
          lineWidth: kind == .localAI ? 1.8 : 1.4
        )
      }
    }
  }

  private func drawHubRings(in context: inout GraphicsContext, around hubRect: CGRect) {
    let centre = CGPoint(x: hubRect.midX, y: hubRect.midY)
    let ringColor = Color(nsColor: .separatorColor).opacity(0.75)
    for (radius, dash) in [(hubRect.width / 2 + 22, [3.0, 4.0]), (hubRect.width / 2 + 66, [])] {
      let ring = Path(
        ellipseIn: CGRect(
          x: centre.x - radius, y: centre.y - radius, width: radius * 2, height: radius * 2))
      context.stroke(
        ring,
        with: .color(ringColor),
        style: StrokeStyle(lineWidth: 1, dash: dash.map { CGFloat($0) }))
    }
  }

  private func drawCurvedArrow(
    in context: inout GraphicsContext,
    from start: CGPoint,
    to end: CGPoint,
    horizontal: Bool,
    color: Color,
    lineWidth: CGFloat
  ) {
    var path = Path()
    path.move(to: start)

    let control1: CGPoint
    let control2: CGPoint
    if horizontal {
      let midX = (start.x + end.x) * 0.5
      control1 = CGPoint(x: midX, y: start.y)
      control2 = CGPoint(x: midX, y: end.y)
    } else {
      let midY = (start.y + end.y) * 0.5
      control1 = CGPoint(x: start.x, y: midY)
      control2 = CGPoint(x: end.x, y: midY)
    }
    path.addCurve(to: end, control1: control1, control2: control2)

    context.stroke(
      path,
      with: .color(color),
      style: StrokeStyle(lineWidth: lineWidth, lineCap: .round)
    )

    // Tangent direction at t = 1 is (end - control2).
    let dx = end.x - control2.x
    let dy = end.y - control2.y
    let length = hypot(dx, dy)
    let ux = length > 0.001 ? dx / length : (horizontal ? 1 : 0)
    let uy = length > 0.001 ? dy / length : (horizontal ? 0 : 1)

    let arrowLength: CGFloat = 6
    let arrowHalfWidth: CGFloat = 3
    let base = CGPoint(x: end.x - ux * arrowLength, y: end.y - uy * arrowLength)
    let left = CGPoint(x: base.x - uy * arrowHalfWidth, y: base.y + ux * arrowHalfWidth)
    let right = CGPoint(x: base.x + uy * arrowHalfWidth, y: base.y - ux * arrowHalfWidth)

    var head = Path()
    head.move(to: end)
    head.addLine(to: left)
    head.addLine(to: right)
    head.closeSubpath()
    context.fill(head, with: .color(color))
  }

  // MARK: - Bottom: Per-App Sqrt-Recency + Power-Law Weekly Curves

  private var weeklyLinesSection: some View {
    let axis = timeAxis

    return GroupBox {
      VStack(spacing: 0) {
        HStack(spacing: TrawlDesign.Space.md) {
          Text("Items per week")
            .font(TrawlDesign.Typography.itemTitle)
            .foregroundStyle(.primary)

          if let hoveredWeek {
            Text(hoverSummary(for: hoveredWeek))
              .font(TrawlDesign.Typography.monoSmall)
              .foregroundStyle(TrawlDesign.Colors.matchRed)
              .lineLimit(1)
              .truncationMode(.tail)
          } else {
            Text("Click a week or drag across a row to pick dates.")
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(.secondary)
              .lineLimit(1)
              .truncationMode(.tail)
          }

          Spacer(minLength: TrawlDesign.Space.sm)

          Picker("Time range", selection: $chartSpan) {
            ForEach(OverviewChartSpan.allCases) { span in
              Text(span.label).tag(span)
            }
          }
          .pickerStyle(.segmented)
          .labelsHidden()
          .controlSize(.small)
          .frame(width: 112)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)

        Divider()

        HStack(spacing: 0) {
          Text("App")
            .font(TrawlDesign.Typography.smallLabel)
            .foregroundStyle(.secondary)
            .frame(width: TrawlDesign.timelineLabelColumnWidth, alignment: .leading)

          GeometryReader { proxy in
            let ticks = axis.yearTicks(forTrackWidth: Double(proxy.size.width))
            ZStack(alignment: .leading) {
              ForEach(ticks) { tick in
                let x = CGFloat(tick.normalizedPosition) * proxy.size.width
                Text(tick.label)
                  .font(TrawlDesign.Typography.smallNumber)
                  .fontWeight(tick.isToday ? .semibold : .regular)
                  .foregroundStyle(tick.isToday ? .primary : .secondary)
                  .fixedSize()
                  .position(
                    x: min(max(x, 16), max(16, proxy.size.width - 18)),
                    y: proxy.size.height / 2
                  )
              }
            }
          }
          .frame(height: 20)

          Text("All time")
            .font(TrawlDesign.Typography.smallLabel)
            .foregroundStyle(.secondary)
            .frame(width: 84, alignment: .trailing)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)

        Divider()

        VStack(spacing: 0) {
          ForEach(Array(datedTrawlers.enumerated()), id: \.element.id) { index, trawler in
            weeklyAppRow(for: trawler, axis: axis)
            if index < datedTrawlers.count - 1 {
              Divider()
                .padding(.leading, 12)
            }
          }
        }

        Divider()

        HStack {
          if hasUndatedContacts {
            Text("Contacts have no dates, so they aren't shown here.")
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(.secondary)
          }
          Spacer()
          Button("Show recent items", action: onBrowseAllItems)
            .buttonStyle(.bordered)
            .controlSize(.small)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
      }
    }
  }

  private func hoverSummary(for hovered: HoveredTimelineWeek) -> String {
    let weekLabel = hovered.weekStart.formatted(.dateTime.year().month(.abbreviated).day())
    let countWord = hovered.recordCount == 1 ? "item" : "items"
    return "\(hovered.trawlerDisplayName) · week of \(weekLabel) · \(hovered.recordCount.formatted()) \(countWord)"
  }

  private func weeklyAppRow(
    for trawler: RestingTrawler,
    axis: ArchiveTimelineTimeAxis
  ) -> some View {
    let status = statusesByID[trawler.id]
    let operability = operabilityByID[trawler.id]
    let buckets = status?.archiveWeeklyHistory?.weeklyRecordCountsInChronologicalOrder ?? []
    let color = iconStore.laneAccentColor(for: trawler.id)
    let isHoveredRow = hoveredWeek?.trawlerID == trawler.id
    let count = operability?.archivedItemCount ?? 0

    return HStack(spacing: 0) {
      Button {
        onBrowseApp(trawler.id)
      } label: {
        HStack(spacing: TrawlDesign.Space.sm) {
          TrawlerIconView(registeredTrawler: trawler.id, size: 20)
          Text(trawler.registeredTrawlerDisplayName)
            .font(TrawlDesign.Typography.laneLabel)
            .foregroundStyle(.primary)
            .lineLimit(1)
          Spacer(minLength: 0)
        }
        .frame(width: TrawlDesign.timelineLabelColumnWidth, alignment: .leading)
        .contentShape(Rectangle())
      }
      .buttonStyle(.plain)

      GeometryReader { proxy in
        let width = max(proxy.size.width, 1)
        ZStack(alignment: .leading) {
          Canvas { context, size in
            drawWeeklyCurve(
              in: &context,
              size: size,
              buckets: buckets,
              axis: axis,
              color: color,
              isHighlighted: isHoveredRow
            )
          }

          if let dragRange, dragRange.trawlerID == trawler.id {
            let low = min(dragRange.startFraction, dragRange.currentFraction)
            let high = max(dragRange.startFraction, dragRange.currentFraction)
            Rectangle()
              .fill(TrawlDesign.Colors.matchRed.opacity(0.18))
              .frame(width: max(3, (high - low) * width), height: proxy.size.height)
              .offset(x: low * width)
          } else if let hoveredWeek, hoveredWeek.trawlerID == trawler.id {
            let x = CGFloat(axis.datedNormalizedPosition(for: hoveredWeek.weekStart)) * width
            Rectangle()
              .fill(color.opacity(0.65))
              .frame(width: 1.5, height: proxy.size.height)
              .offset(x: min(max(x, 0), max(0, width - 1.5)))
          }
        }
        .contentShape(Rectangle())
        .onContinuousHover { phase in
          switch phase {
          case .active(let location):
            let frac = Double(min(max(location.x / width, 0), 1))
            let hoveredDate = axis.date(forDatedNormalizedPosition: frac)
            let nearest = nearestBucket(to: hoveredDate, in: buckets)
            let snappedWeekStart = nearest?.utcWeekStartTime ?? utcWeekStart(for: hoveredDate)
            hoveredWeek = HoveredTimelineWeek(
              trawlerID: trawler.id,
              trawlerDisplayName: trawler.registeredTrawlerDisplayName,
              weekStart: snappedWeekStart,
              recordCount: nearest?.recordCount ?? 0
            )
          case .ended:
            if hoveredWeek?.trawlerID == trawler.id {
              hoveredWeek = nil
            }
          }
        }
        .gesture(
          DragGesture(minimumDistance: 0)
            .onChanged { value in
              let s = min(max(value.startLocation.x / width, 0), 1)
              let c = min(max(value.location.x / width, 0), 1)
              dragRange = TimelineDragSelection(
                trawlerID: trawler.id,
                startFraction: s,
                currentFraction: c
              )
            }
            .onEnded { value in
              let s = min(max(value.startLocation.x / width, 0), 1)
              let e = min(max(value.location.x / width, 0), 1)
              dragRange = nil
              if abs(e - s) * width < 6 {
                let clickedDate = axis.date(forDatedNormalizedPosition: Double(e))
                let weekStart: Date
                if let hoveredWeek, hoveredWeek.trawlerID == trawler.id {
                  weekStart = hoveredWeek.weekStart
                } else if let nearest = nearestBucket(to: clickedDate, in: buckets) {
                  weekStart = nearest.utcWeekStartTime
                } else {
                  weekStart = utcWeekStart(for: clickedDate)
                }
                let weekEnd = weekStart.addingTimeInterval(7 * 86_400 - 1)
                onSelectDateRange(trawler.id, weekStart, min(weekEnd, Date()))
              } else {
                let range = axis.inclusiveCalendarDayRange(
                  fromNormalizedPosition: Double(min(s, e)),
                  toNormalizedPosition: Double(max(s, e))
                )
                onSelectDateRange(trawler.id, range.earliestInclusive, range.latestInclusive)
              }
            }
        )
      }
      .frame(height: TrawlDesign.timelineOverviewLaneHeight)

      Text(count.formatted())
        .font(TrawlDesign.Typography.monoSmall)
        .foregroundStyle(.secondary)
        .frame(width: 84, alignment: .trailing)
    }
    .padding(.horizontal, 12)
    .padding(.vertical, 6)
    .background(isHoveredRow ? TrawlDesign.Colors.matchRed.opacity(0.05) : Color.clear)
  }

  private func utcWeekStart(for date: Date) -> Date {
    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(secondsFromGMT: 0) ?? .gmt
    let weekday = calendar.component(.weekday, from: date)
    let daysSinceMonday = (weekday + 5) % 7
    let dayStart = calendar.startOfDay(for: date)
    return calendar.date(byAdding: .day, value: -daysSinceMonday, to: dayStart) ?? dayStart
  }

  private func nearestBucket(
    to date: Date,
    in buckets: [ArchiveWeeklyRecordCount]
  ) -> ArchiveWeeklyRecordCount? {
    var best: ArchiveWeeklyRecordCount?
    var bestDistance = TimeInterval.greatestFiniteMagnitude
    for bucket in buckets {
      let center = bucket.utcWeekStartTime.addingTimeInterval(3.5 * 86_400)
      let distance = abs(center.timeIntervalSince(date))
      if distance < bestDistance {
        bestDistance = distance
        best = bucket
      }
    }
    guard bestDistance <= 14 * 86_400 else { return nil }
    return best
  }

  private func drawWeeklyCurve(
    in context: inout GraphicsContext,
    size: CGSize,
    buckets: [ArchiveWeeklyRecordCount],
    axis: ArchiveTimelineTimeAxis,
    color: Color,
    isHighlighted: Bool
  ) {
    let baselineY = size.height - 2
    var baseline = Path()
    baseline.move(to: CGPoint(x: 0, y: baselineY))
    baseline.addLine(to: CGPoint(x: size.width, y: baselineY))
    context.stroke(
      baseline,
      with: .color(Color(nsColor: .separatorColor).opacity(0.55)),
      lineWidth: 0.75
    )

    let visible = buckets.filter {
      $0.utcWeekStartTime >= axis.earliestAxisDate && $0.utcWeekStartTime <= axis.referenceDate
    }
    guard !visible.isEmpty else { return }

    let rowMax = visible.map(\.recordCount).max() ?? 0
    guard rowMax > 0 else { return }

    let usableHeight = max(size.height - 5, 4)

    var strokePath = Path()
    var fillPath = Path()
    var previousWeekStart: Date?
    var previousX: CGFloat = 0
    var hasStarted = false

    for bucket in visible {
      let x = CGFloat(axis.datedNormalizedPosition(for: bucket.utcWeekStartTime)) * size.width
      let fraction = ArchiveTimelineIntensityScale.heightFraction(
        forRecordCount: bucket.recordCount,
        rowMaximumRecordCount: rowMax
      )
      let y = bucket.recordCount > 0
        ? baselineY - max(1.5, CGFloat(fraction) * usableHeight)
        : baselineY
      let point = CGPoint(x: x, y: y)

      if !hasStarted {
        hasStarted = true
        strokePath.move(to: point)
        fillPath.move(to: CGPoint(x: x, y: baselineY))
        fillPath.addLine(to: point)
      } else if let prev = previousWeekStart,
        bucket.utcWeekStartTime.timeIntervalSince(prev) > 21 * 86_400
      {
        let dropX = min(x, previousX + 2)
        let riseX = max(dropX, x - 2)
        strokePath.addLine(to: CGPoint(x: dropX, y: baselineY))
        strokePath.addLine(to: CGPoint(x: riseX, y: baselineY))
        strokePath.addLine(to: point)
        fillPath.addLine(to: CGPoint(x: dropX, y: baselineY))
        fillPath.addLine(to: CGPoint(x: riseX, y: baselineY))
        fillPath.addLine(to: point)
      } else {
        strokePath.addLine(to: point)
        fillPath.addLine(to: point)
      }
      previousWeekStart = bucket.utcWeekStartTime
      previousX = x
    }

    guard hasStarted else { return }
    fillPath.addLine(to: CGPoint(x: previousX, y: baselineY))
    fillPath.closeSubpath()

    context.fill(fillPath, with: .color(color.opacity(isHighlighted ? 0.24 : 0.16)))
    context.stroke(
      strokePath,
      with: .color(color.opacity(isHighlighted ? 0.95 : 0.80)),
      style: StrokeStyle(lineWidth: isHighlighted ? 1.8 : 1.35, lineJoin: .round)
    )
  }
}

private enum OverviewChartSpan: String, CaseIterable, Identifiable {
  case oneYear
  case fiveYears
  case all

  var id: String { rawValue }

  var label: String {
    switch self {
    case .oneYear: "1Y"
    case .fiveYears: "5Y"
    case .all: "All"
    }
  }
}

private struct HoveredTimelineWeek: Equatable {
  let trawlerID: RegisteredTrawlerIdentity
  let trawlerDisplayName: String
  let weekStart: Date
  let recordCount: UInt64
}

private struct TimelineDragSelection: Equatable {
  let trawlerID: RegisteredTrawlerIdentity
  let startFraction: CGFloat
  let currentFraction: CGFloat
}
