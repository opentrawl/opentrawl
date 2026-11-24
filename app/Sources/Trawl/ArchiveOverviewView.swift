import SwiftUI
import TrawlClient
import TrawlCore

/// Overview (Home) view:
/// 1. Top: "Your archive" — calm, spacious single-row connection strip from the archive
///    host hub to local AI, cloud AI, and other Macs (never duplicating the 12-app list
///    already shown in the left rail and weekly curves).
/// 2. Bottom: "Items per week" — fixed-height weekly curves using the sqrt-recency time axis
///    (`ArchiveTimelineTimeAxis`) and power-law vertical intensity (`ArchiveTimelineIntensityScale`).
struct ArchiveOverviewView: View {
  @Environment(TrawlerIconStore.self) private var iconStore

  let windowWidth: CGFloat
  let trawlers: [RestingTrawler]
  let statusesByID: [RegisteredTrawlerIdentity: TrawlerStatus]
  let operabilityByID: [RegisteredTrawlerIdentity: AppOperabilityState]
  let hostDisplayName: String
  let freshnessLine: String
  let totalItemCount: UInt64
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

  private var attentionTrawlers: [(RestingTrawler, AppOperabilityState)] {
    trawlers.compactMap { trawler in
      guard let op = operabilityByID[trawler.id], op.needsAttention, op.action != nil else {
        return nil
      }
      return (trawler, op)
    }
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
      ScrollView {
        VStack(alignment: .leading, spacing: TrawlDesign.Space.lg) {
          archiveSummarySection
          weeklyLinesCard
        }
        .padding(TrawlDesign.Space.lg)
        .frame(width: outerProxy.size.width, alignment: .topLeading)
      }
      .background(TrawlDesign.Colors.surface)
    }
  }

  // MARK: - Top: Your archive summary & connections (zero app-list duplication)

  private var archiveSummarySection: some View {
    let usesHorizontalStrip = windowWidth >= 1_040

    return VStack(alignment: .leading, spacing: TrawlDesign.Space.md) {
      HStack(alignment: .firstTextBaseline) {
        Text("Your archive")
          .font(TrawlDesign.Typography.sectionHeader)
          .foregroundStyle(TrawlDesign.Colors.ink)
        Spacer()
        if !attentionTrawlers.isEmpty {
          HStack(spacing: TrawlDesign.Space.sm) {
            ForEach(attentionTrawlers.prefix(2), id: \.0.id) { trawler, op in
              if let title = op.actionTitle, let action = op.action {
                Button("\(trawler.registeredTrawlerDisplayName): \(title)") {
                  onRunAttentionAction(trawler.id, action)
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .tint(TrawlDesign.Colors.warningAmber)
              }
            }
          }
        }
      }

      if usesHorizontalStrip {
        HStack(alignment: .center, spacing: 0) {
          centerHostHub
            .frame(width: 216)

          ConstellationConnectorColumn(branchCount: isRemoteArchive ? 1 : 3)
            .frame(width: 36)
            .frame(maxHeight: .infinity)

          connectionCardsHorizontal
        }
        .fixedSize(horizontal: false, vertical: true)
      } else {
        VStack(alignment: .leading, spacing: TrawlDesign.Space.sm) {
          centerHostHub
          connectionCardsHorizontal
        }
        .fixedSize(horizontal: false, vertical: true)
      }
    }
  }

  private var centerHostHub: some View {
    Button(action: onOpenHostSwitcher) {
      HStack(spacing: TrawlDesign.Space.md) {
        OpenTrawlMarkIcon(size: 32)
        VStack(alignment: .leading, spacing: 2) {
          HStack(spacing: TrawlDesign.Space.xs) {
            Text(hostDisplayName)
              .font(TrawlDesign.Typography.cardTitle)
              .foregroundStyle(TrawlDesign.Colors.ink)
              .lineLimit(1)
            Image(systemName: "chevron.up.chevron.down")
              .font(TrawlDesign.Typography.monoAxis)
              .foregroundStyle(TrawlDesign.Colors.mutedInk)
          }
          Text("\(totalItemCount.formatted()) items")
            .font(TrawlDesign.Typography.monoSmall)
            .monospacedDigit()
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
            .lineLimit(1)
        }
        Spacer(minLength: 0)
      }
      .padding(TrawlDesign.Space.md)
      .frame(maxWidth: .infinity, alignment: .leading)
      .background(TrawlDesign.Colors.paper, in: TrawlDesign.Shape.surface)
      .overlay(TrawlDesign.Shape.surface.strokeBorder(TrawlDesign.Colors.hairline, lineWidth: 1))
    }
    .buttonStyle(.plain)
    .help("Switch archive host")
  }

  private var connectionCardsHorizontal: some View {
    HStack(alignment: .top, spacing: TrawlDesign.Space.sm) {
      if !isRemoteArchive {
        connectionCard(
          title: "AI apps on this Mac",
          subtitle: "Claude Code, Codex, Cursor and others",
          buttonTitle: hasCopiedAIPrompt ? "Copied" : HumanCopy.ConnectAI.copyAction,
          isProminent: true,
          action: onCopyAIPrompt
        )
        connectionCard(
          title: "AI on web or phone",
          subtitle: "Let Claude, ChatGPT or Grok search this archive",
          buttonTitle: "Set up…",
          isProminent: false,
          action: onOpenAIAppsSettings
        )
      }
      connectionCard(
        title: "Other Macs",
        subtitle: isRemoteArchive
          ? "Reading archive on \(hostDisplayName)"
          : "Search this archive from another Mac",
        buttonTitle: "Manage…",
        isProminent: false,
        action: onOpenOtherMacsSettings
      )
    }
    .frame(maxWidth: .infinity)
  }

  private func connectionCard(
    title: String,
    subtitle: String,
    buttonTitle: String,
    isProminent: Bool,
    action: @escaping () -> Void
  ) -> some View {
    VStack(alignment: .leading, spacing: TrawlDesign.Space.sm) {
      VStack(alignment: .leading, spacing: TrawlDesign.Space.xs) {
        Text(title)
          .font(TrawlDesign.Typography.cardTitle)
          .foregroundStyle(TrawlDesign.Colors.ink)
          .lineLimit(1)
        Text(subtitle)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
          .fixedSize(horizontal: false, vertical: true)
      }
      Spacer(minLength: 0)
      if isProminent {
        Button(buttonTitle, action: action)
          .buttonStyle(.borderedProminent)
          .tint(TrawlDesign.Colors.selectBlue)
          .controlSize(.small)
      } else {
        Button(buttonTitle, action: action)
          .buttonStyle(.bordered)
          .controlSize(.small)
      }
    }
    .padding(TrawlDesign.Space.md)
    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    .background(TrawlDesign.Colors.paper, in: TrawlDesign.Shape.surface)
    .overlay(TrawlDesign.Shape.surface.strokeBorder(TrawlDesign.Colors.hairline, lineWidth: 1))
  }

  // MARK: - Bottom: Per-App Sqrt-Recency + Power-Law Weekly Curves

  private var weeklyLinesCard: some View {
    let axis = timeAxis

    return VStack(spacing: 0) {
      HStack(spacing: TrawlDesign.Space.md) {
        Text("Items per week")
          .font(TrawlDesign.Typography.sectionHeader)
          .foregroundStyle(TrawlDesign.Colors.ink)

        // Fixed single-line readout slot so hovering never reflows headers or rows
        Group {
          if let hovered = hoveredWeek {
            Text(
              "\(hovered.trawlerName) · week of \(hovered.weekStart.formatted(.dateTime.day().month(.abbreviated).year())) · \(hovered.count.formatted()) items"
            )
            .font(TrawlDesign.Typography.meta)
            .monospacedDigit()
            .foregroundStyle(TrawlDesign.Colors.matchRed)
          } else {
            Text("Click a week or drag across a row to pick dates.")
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(TrawlDesign.Colors.mutedInk)
          }
        }
        .lineLimit(1)
        .truncationMode(.tail)

        Spacer(minLength: TrawlDesign.Space.sm)

        Picker("Time range", selection: $chartSpan) {
          ForEach(OverviewChartSpan.allCases) { span in
            Text(span.rawValue).tag(span)
          }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .controlSize(.small)
        .frame(width: 116)
      }
      .padding(.horizontal, TrawlDesign.Space.md)
      .padding(.vertical, TrawlDesign.Space.sm)

      Divider()

      HStack(spacing: TrawlDesign.Space.md) {
        Text("App")
          .font(TrawlDesign.Typography.monoAxis)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
          .frame(width: 120, alignment: .leading)

        GeometryReader { proxy in
          let ticks = axis.yearTicks(forTrackWidth: Double(proxy.size.width))
          ZStack(alignment: .leading) {
            ForEach(ticks) { tick in
              let x = CGFloat(tick.normalizedPosition) * proxy.size.width
              Text(tick.label)
                .font(TrawlDesign.Typography.monoAxis)
                .monospacedDigit()
                .foregroundStyle(tick.isToday ? TrawlDesign.Colors.ink : TrawlDesign.Colors.mutedInk)
                .fixedSize()
                .position(
                  x: min(max(x, 16), max(16, proxy.size.width - 18)),
                  y: proxy.size.height / 2
                )
            }
          }
        }
        .frame(height: 24)

        Text("Total")
          .font(TrawlDesign.Typography.monoAxis)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
          .frame(width: 72, alignment: .trailing)
      }
      .padding(.horizontal, TrawlDesign.Space.md)
      .frame(height: 26)
      .background(TrawlDesign.Colors.surface)

      Divider()

      VStack(spacing: 0) {
        ForEach(datedTrawlers) { trawler in
          OverviewWeeklyCurveRow(
            trawler: trawler,
            status: statusesByID[trawler.id],
            totalCount: operabilityByID[trawler.id]?.archivedItemCount ?? 0,
            accentColor: iconStore.laneAccentColor(for: trawler.id),
            axis: axis,
            hoveredWeek: $hoveredWeek,
            dragRange: $dragRange,
            onBrowseApp: { onBrowseApp(trawler.id) },
            onSelectDateRange: { start, end in
              onSelectDateRange(trawler.id, start, end)
            }
          )
          Divider()
            .opacity(0.65)
        }
      }

      HStack {
        if hasUndatedContacts {
          Text("Contacts have no dates, so they aren't shown.")
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
        Spacer()
        Button("Show recent items →", action: onBrowseAllItems)
          .buttonStyle(.plain)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.selectBlue)
      }
      .padding(.horizontal, TrawlDesign.Space.md)
      .padding(.vertical, TrawlDesign.Space.sm)
      .background(TrawlDesign.Colors.surface)
    }
    .background(TrawlDesign.Colors.paper, in: TrawlDesign.Shape.surface)
    .clipShape(TrawlDesign.Shape.surface)
    .overlay(TrawlDesign.Shape.surface.strokeBorder(TrawlDesign.Colors.hairline, lineWidth: 1))
  }
}

private struct HoveredTimelineWeek: Equatable {
  let trawlerID: RegisteredTrawlerIdentity
  let trawlerName: String
  let weekStart: Date
  let count: UInt64
  let normalizedX: CGFloat
}

private struct TimelineDragSelection: Equatable {
  let trawlerID: RegisteredTrawlerIdentity
  let startFraction: CGFloat
  let currentFraction: CGFloat

  var lowerFraction: CGFloat { min(startFraction, currentFraction) }
  var upperFraction: CGFloat { max(startFraction, currentFraction) }
}

private struct OverviewWeeklyCurveRow: View {
  let trawler: RestingTrawler
  let status: TrawlerStatus?
  let totalCount: UInt64
  let accentColor: Color
  let axis: ArchiveTimelineTimeAxis
  @Binding var hoveredWeek: HoveredTimelineWeek?
  @Binding var dragRange: TimelineDragSelection?
  let onBrowseApp: () -> Void
  let onSelectDateRange: (Date, Date) -> Void

  private var visibleBuckets: [ArchiveWeeklyRecordCount] {
    guard let buckets = status?.archiveWeeklyHistory?.weeklyRecordCountsInChronologicalOrder else {
      return []
    }
    return buckets.filter { bucket in
      bucket.utcWeekStartTime >= axis.earliestAxisDate && bucket.utcWeekStartTime <= axis.referenceDate
    }
  }

  private var isHoveredRow: Bool {
    hoveredWeek?.trawlerID == trawler.id
  }

  var body: some View {
    HStack(spacing: TrawlDesign.Space.md) {
      Button(action: onBrowseApp) {
        HStack(spacing: TrawlDesign.Space.sm) {
          TrawlerIconView(registeredTrawler: trawler.id, size: 18)
          Text(trawler.registeredTrawlerDisplayName)
            .font(TrawlDesign.Typography.laneLabel)
            .foregroundStyle(TrawlDesign.Colors.ink)
            .lineLimit(1)
        }
        .frame(width: 120, alignment: .leading)
        .contentShape(Rectangle())
      }
      .buttonStyle(.plain)

      GeometryReader { proxy in
        let width = max(proxy.size.width, 1)
        let height = proxy.size.height

        ZStack(alignment: .leading) {
          Canvas { context, size in
            drawPowerLawSqrtCurve(in: &context, size: size)
          }

          if let drag = dragRange, drag.trawlerID == trawler.id {
            let x1 = drag.lowerFraction * width
            let x2 = drag.upperFraction * width
            Rectangle()
              .fill(TrawlDesign.Colors.matchRed.opacity(0.16))
              .frame(width: max(2, x2 - x1), height: height)
              .offset(x: x1)
          }

          if let hovered = hoveredWeek {
            let x = hovered.normalizedX * width
            Rectangle()
              .fill(TrawlDesign.Colors.matchRed.opacity(isHoveredRow ? 0.9 : 0.25))
              .frame(width: isHoveredRow ? 1.5 : 1, height: height)
              .offset(x: x)
          }
        }
        .contentShape(Rectangle())
        .onContinuousHover { phase in
          switch phase {
          case .active(let location):
            let frac = min(max(location.x / width, 0), 1)
            updateHover(fraction: frac)
          case .ended:
            if hoveredWeek?.trawlerID == trawler.id {
              hoveredWeek = nil
            }
          }
        }
        .gesture(
          DragGesture(minimumDistance: 0)
            .onChanged { value in
              let startFrac = min(max(value.startLocation.x / width, 0), 1)
              let curFrac = min(max(value.location.x / width, 0), 1)
              if abs(curFrac - startFrac) * width > 6 {
                dragRange = TimelineDragSelection(
                  trawlerID: trawler.id,
                  startFraction: startFrac,
                  currentFraction: curFrac
                )
              }
              updateHover(fraction: curFrac)
            }
            .onEnded { value in
              let startFrac = min(max(value.startLocation.x / width, 0), 1)
              let endFrac = min(max(value.location.x / width, 0), 1)
              dragRange = nil
              if abs(endFrac - startFrac) * width > 6 {
                let range = axis.inclusiveCalendarDayRange(
                  fromNormalizedPosition: Double(min(startFrac, endFrac)),
                  toNormalizedPosition: Double(max(startFrac, endFrac))
                )
                onSelectDateRange(range.earliestInclusive, range.latestInclusive)
              } else {
                // Use the exact same UTC week bucket shown in hover readout
                let targetDate = axis.date(forDatedNormalizedPosition: Double(endFrac))
                let weekStartUnix = ArchiveTimelineTimeAxis.utcWeekStartUnixSeconds(for: targetDate)
                let weekStart = Date(timeIntervalSince1970: TimeInterval(weekStartUnix))
                onSelectDateRange(weekStart, weekStart.addingTimeInterval(7 * 86_400 - 1))
              }
            }
        )
      }
      .frame(height: 24)

      Text(totalCount.formatted())
        .font(TrawlDesign.Typography.monoSmall)
        .monospacedDigit()
        .foregroundStyle(TrawlDesign.Colors.mutedInk)
        .frame(width: 72, alignment: .trailing)
    }
    .padding(.horizontal, TrawlDesign.Space.md)
    .frame(height: 34)
    .background(isHoveredRow ? TrawlDesign.Colors.matchRed.opacity(0.05) : Color.clear)
  }

  private func updateHover(fraction: CGFloat) {
    let targetDate = axis.date(forDatedNormalizedPosition: Double(fraction))
    let weekStartUnix = ArchiveTimelineTimeAxis.utcWeekStartUnixSeconds(for: targetDate)
    let weekStart = Date(timeIntervalSince1970: TimeInterval(weekStartUnix))
    let matchingBucket = visibleBuckets.first { $0.utcWeekStartUnixSeconds == weekStartUnix }
    hoveredWeek = HoveredTimelineWeek(
      trawlerID: trawler.id,
      trawlerName: trawler.registeredTrawlerDisplayName,
      weekStart: weekStart,
      count: matchingBucket?.recordCount ?? 0,
      normalizedX: fraction
    )
  }

  private func drawPowerLawSqrtCurve(in context: inout GraphicsContext, size: CGSize) {
    let baselineY = size.height - 2
    var baseline = Path()
    baseline.move(to: CGPoint(x: 0, y: baselineY))
    baseline.addLine(to: CGPoint(x: size.width, y: baselineY))
    context.stroke(baseline, with: .color(TrawlDesign.Colors.hairline.opacity(0.55)), lineWidth: 0.75)

    let buckets = visibleBuckets
    guard !buckets.isEmpty else { return }
    let rowMax = buckets.map(\.recordCount).max() ?? 0
    guard rowMax > 0 else { return }

    let usableHeight = max(size.height - 4, 4)

    var strokePath = Path()
    var fillPath = Path()
    var previousWeekStart: Date?
    var previousX: CGFloat = 0
    var hasStarted = false

    for bucket in buckets {
      let x = CGFloat(axis.datedNormalizedPosition(for: bucket.utcWeekStartTime)) * size.width
      let heightFraction = ArchiveTimelineIntensityScale.heightFraction(
        forRecordCount: bucket.recordCount,
        rowMaximumRecordCount: rowMax
      )
      let y = bucket.recordCount > 0
        ? baselineY - max(1.5, CGFloat(heightFraction) * usableHeight)
        : baselineY
      let pt = CGPoint(x: x, y: y)

      if !hasStarted {
        hasStarted = true
        strokePath.move(to: pt)
        fillPath.move(to: CGPoint(x: x, y: baselineY))
        fillPath.addLine(to: pt)
      } else if let prev = previousWeekStart,
        bucket.utcWeekStartTime.timeIntervalSince(prev) > 21 * 86_400
      {
        let dropX = min(x, previousX + 2)
        let riseX = max(dropX, x - 2)
        strokePath.addLine(to: CGPoint(x: dropX, y: baselineY))
        strokePath.addLine(to: CGPoint(x: riseX, y: baselineY))
        strokePath.addLine(to: pt)
        fillPath.addLine(to: CGPoint(x: dropX, y: baselineY))
        fillPath.addLine(to: CGPoint(x: riseX, y: baselineY))
        fillPath.addLine(to: pt)
      } else {
        strokePath.addLine(to: pt)
        fillPath.addLine(to: pt)
      }
      previousWeekStart = bucket.utcWeekStartTime
      previousX = x
    }

    if hasStarted {
      fillPath.addLine(to: CGPoint(x: previousX, y: baselineY))
      fillPath.closeSubpath()
      context.fill(fillPath, with: .color(accentColor.opacity(0.18)))
      context.stroke(
        strokePath,
        with: .color(accentColor.opacity(0.92)),
        style: StrokeStyle(lineWidth: 1.4, lineJoin: .round)
      )
    }
  }
}

private struct ConstellationConnectorColumn: View {
  let branchCount: Int

  var body: some View {
    Canvas { context, size in
      let centerY = size.height / 2
      let w = size.width
      var path = Path()
      path.move(to: CGPoint(x: 0, y: centerY))
      path.addLine(to: CGPoint(x: w, y: centerY))
      context.stroke(
        path,
        with: .color(TrawlDesign.Colors.hairline),
        style: StrokeStyle(lineWidth: 1.2, lineCap: .round)
      )
    }
  }
}
