import SwiftUI
import TrawlClient
import TrawlCore

struct StrataBandView: View {
  @Environment(TrawlerIconStore.self) private var icons
  @Environment(\.accessibilityReduceMotion) private var reduceMotion
  @Environment(\.locale) private var locale

  let trawlers: [RestingTrawler]
  let statusesByID: [RegisteredTrawlerIdentity: TrawlerStatus]
  let detailOverrides: [RegisteredTrawlerIdentity: String]
  let updatingTrawlers: Set<RegisteredTrawlerIdentity>
  let isSearchActive: Bool
  let selectedTrawlerID: RegisteredTrawlerIdentity?
  let earliestDateInclusive: Date?
  let latestDateInclusive: Date?
  let searchMatches: [SearchMatch]
  let trawlerSearchResults: [TrawlerSearchResult]
  let searchFailures: [TrawlerOperationFailure]
  let searchSkips: [TrawlerSkippedFromOperation]
  let selectedMatchID: SearchMatchIdentifier?
  let restingAvailableLanesHeight: CGFloat?
  let maxSearchLanesHeight: CGFloat?
  let onSelectTrawler: (RegisteredTrawlerIdentity?) -> Void
  let onBrushDateRange: (Date, Date, RegisteredTrawlerIdentity?) -> Void
  let onSelectMatch: (SearchMatch) -> Void

  @State private var activeBrush: StrataDragBrush?

  private struct StrataDragBrush: Equatable {
    let startNormalizedX: Double
    var currentNormalizedX: Double
    let targetTrawlerID: RegisteredTrawlerIdentity?
  }

  private var axis: StrataTimeAxis {
    let histories = trawlers.compactMap { statusesByID[$0.id]?.archiveMonthlyHistory }
    return StrataTimeAxis.build(from: histories)
  }

  private var hasAnyUndatedRecords: Bool {
    trawlers.contains { trawler in
      (statusesByID[trawler.id]?.archiveMonthlyHistory?.undatedRecordCount ?? 0) > 0
    }
  }

  private var searchResultsByTrawler: [RegisteredTrawlerIdentity: TrawlerSearchResult] {
    Dictionary(
      uniqueKeysWithValues: trawlerSearchResults.map { ($0.registeredTrawler, $0) }
    )
  }

  private var failedSearchTrawlers: Set<RegisteredTrawlerIdentity> {
    Set(searchFailures.map(\.failedTrawler))
  }

  private var skippedSearchTrawlers: Set<RegisteredTrawlerIdentity> {
    Set(searchSkips.map(\.skippedTrawler))
  }

  private var matchesByTrawler: [RegisteredTrawlerIdentity: [SearchMatch]] {
    var grouped: [RegisteredTrawlerIdentity: [SearchMatch]] = [:]
    for match in searchMatches {
      guard let trawler = match.registeredTrawler else { continue }
      grouped[trawler, default: []].append(match)
    }
    return grouped
  }

  private var highlightedNormalizedRange: ClosedRange<Double>? {
    if let activeBrush {
      let low = min(activeBrush.startNormalizedX, activeBrush.currentNormalizedX)
      let high = max(activeBrush.startNormalizedX, activeBrush.currentNormalizedX)
      return low...high
    }
    guard earliestDateInclusive != nil || latestDateInclusive != nil else { return nil }
    let low = earliestDateInclusive.map { axis.datedNormalizedPosition(for: $0) } ?? 0.0
    let high = latestDateInclusive.map { axis.datedNormalizedPosition(for: $0) } ?? 1.0
    return min(low, high)...max(low, high)
  }

  private var liveBrushDatePreviewLabel: String? {
    guard let activeBrush else { return nil }
    let range = axis.inclusiveCalendarDayRange(
      fromNormalizedPosition: activeBrush.startNormalizedX,
      toNormalizedPosition: activeBrush.currentNormalizedX
    )
    return StrataDateRangeFormatting.label(
      earliestInclusive: range.earliestInclusive,
      latestInclusive: range.latestInclusive,
      locale: locale
    )
  }

  private var restingLaneHeight: CGFloat {
    guard !isSearchActive, let available = restingAvailableLanesHeight, !trawlers.isEmpty else {
      return TrawlDesign.strataLaneHeight
    }
    let perLane = floor(available / CGFloat(trawlers.count))
    return min(
      TrawlDesign.strataRestingLaneMaximumHeight,
      max(TrawlDesign.strataRestingLaneMinimumHeight, perLane)
    )
  }

  var body: some View {
    VStack(spacing: 0) {
      axisHeaderRow
      Rectangle()
        .fill(TrawlDesign.Colors.hairline)
        .frame(height: 1)
      lanesContainer
    }
    .background(TrawlDesign.Colors.paper)
  }

  private var axisHeaderRow: some View {
    HStack(spacing: 0) {
      intensityLegend
        .padding(.leading, TrawlDesign.Space.md)
        .padding(.trailing, TrawlDesign.Space.sm)
        .frame(width: TrawlDesign.strataLabelColumnWidth, alignment: .leading)

      GeometryReader { proxy in
        let geometry = StrataTrackGeometry(
          totalWidth: proxy.size.width,
          showsUndatedColumn: hasAnyUndatedRecords
        )
        let ticks = axis.yearTicks(forTrackWidth: Double(geometry.datedWidth))
        ZStack(alignment: .leading) {
          if let range = highlightedNormalizedRange {
            let x0 = geometry.datedMinX + CGFloat(range.lowerBound) * geometry.datedWidth
            let x1 = geometry.datedMinX + CGFloat(range.upperBound) * geometry.datedWidth
            Rectangle()
              .fill(TrawlDesign.Colors.selectBlueSurface)
              .frame(width: max(2, x1 - x0), height: TrawlDesign.strataAxisHeight)
              .offset(x: x0)
          }

          if hasAnyUndatedRecords {
            Text("∞")
              .font(TrawlDesign.Typography.monoAxis)
              .foregroundStyle(TrawlDesign.Colors.mutedInk)
              .frame(width: geometry.undatedWidth, alignment: .center)
              .offset(x: geometry.undatedMinX)
              .help("Undated records")
          }

          ForEach(ticks) { tick in
            let tickX = geometry.datedMinX + CGFloat(tick.normalizedPosition) * geometry.datedWidth
            Text(tick.label)
              .font(TrawlDesign.Typography.monoAxis)
              .foregroundStyle(
                tick.isToday ? TrawlDesign.Colors.ink : TrawlDesign.Colors.mutedInk
              )
              .fixedSize()
              .position(
                x: min(max(tickX, geometry.datedMinX + 16), geometry.datedMaxX - 18),
                y: TrawlDesign.strataAxisHeight / 2
              )
          }

          Text("Next")
            .font(TrawlDesign.Typography.monoAxis)
            .foregroundStyle(TrawlDesign.Colors.mutedInk.opacity(0.8))
            .frame(width: geometry.futureWidth, alignment: .center)
            .offset(x: geometry.futureMinX)
            .help("Upcoming calendar events")

          if let liveBrushDatePreviewLabel, let activeBrush {
            let midU = (activeBrush.startNormalizedX + activeBrush.currentNormalizedX) / 2
            let midX = geometry.datedMinX + CGFloat(midU) * geometry.datedWidth
            Text(liveBrushDatePreviewLabel)
              .font(TrawlDesign.Typography.monoAxis)
              .foregroundStyle(.white)
              .padding(.horizontal, 7)
              .padding(.vertical, 2)
              .background(TrawlDesign.Colors.selectBlue, in: Capsule())
              .fixedSize()
              .position(
                x: min(max(midX, geometry.datedMinX + 60), geometry.datedMaxX - 60),
                y: TrawlDesign.strataAxisHeight / 2
              )
          }
        }
        .contentShape(.rect)
        .gesture(axisBrushGesture(geometry: geometry))
      }
      .frame(height: TrawlDesign.strataAxisHeight)

      Text(isSearchActive ? "Matches" : "Records")
        .font(TrawlDesign.Typography.monoAxis)
        .foregroundStyle(TrawlDesign.Colors.mutedInk)
        .padding(.trailing, TrawlDesign.Space.md)
        .frame(width: TrawlDesign.strataCountColumnWidth, alignment: .trailing)
    }
    .frame(height: TrawlDesign.strataAxisHeight)
    .accessibilityElement(children: .combine)
    .accessibilityLabel("Archive timeline axis. Use the Dates control in the search bar or drag across the dated timeline to filter by date range.")
  }

  private var intensityLegend: some View {
    HStack(spacing: 6) {
      ForEach(StrataIntensityScale.legendReferenceCounts, id: \.self) { count in
        let barHeight = CGFloat(StrataIntensityScale.barHeightFraction(forRecordCount: count)) * 12
        let opacity = StrataIntensityScale.fillOpacity(forRecordCount: count)
        HStack(spacing: 3) {
          Rectangle()
            .fill(TrawlDesign.Colors.ink.opacity(opacity))
            .frame(width: 4, height: max(2, barHeight))
          Text(legendLabel(for: count))
            .font(TrawlDesign.Typography.monoAxis)
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
      }
    }
    .accessibilityLabel("Monthly volume scale: 10, 1 thousand, and 10 thousand records per month.")
  }

  private func legendLabel(for count: UInt64) -> String {
    switch count {
    case 1_000: "1k"
    case 10_000: "10k"
    default: "\(count)"
    }
  }

  @ViewBuilder
  private var lanesContainer: some View {
    let baseRestingHeight = restingLaneHeight
    let content = VStack(spacing: 0) {
      ForEach(trawlers) { trawler in
        let isFocused = selectedTrawlerID == trawler.id
        let isSlimmed = isSearchActive && selectedTrawlerID != nil && !isFocused
        let laneHeight: CGFloat =
          isSearchActive
          ? (isSlimmed ? TrawlDesign.strataSlimLaneHeight : TrawlDesign.strataLaneHeight)
          : baseRestingHeight
        StrataLaneRow(
          trawler: trawler,
          status: statusesByID[trawler.id],
          detailOverride: detailOverrides[trawler.id],
          accentColor: icons.laneAccentColor(for: trawler.id),
          axis: axis,
          showsUndatedColumn: hasAnyUndatedRecords,
          laneHeight: laneHeight,
          isRestingExpanded: !isSearchActive,
          isFocused: isFocused,
          isSlimmed: isSlimmed,
          isUpdating: updatingTrawlers.contains(trawler.id),
          isSearchActive: isSearchActive,
          highlightedRange: highlightedNormalizedRange,
          isBrushTarget: activeBrush?.targetTrawlerID == trawler.id,
          laneMatches: matchesByTrawler[trawler.id] ?? [],
          searchResult: searchResultsByTrawler[trawler.id],
          searchFailed: failedSearchTrawlers.contains(trawler.id),
          searchSkipped: skippedSearchTrawlers.contains(trawler.id),
          isExcludedByScope: selectedTrawlerID != nil && selectedTrawlerID != trawler.id,
          selectedMatchID: selectedMatchID,
          onToggleScope: {
            guard trawler.state != .comingSoon else { return }
            onSelectTrawler(isFocused ? nil : trawler.id)
          },
          onBrushChanged: { startU, currentU in
            activeBrush = StrataDragBrush(
              startNormalizedX: startU,
              currentNormalizedX: currentU,
              targetTrawlerID: trawler.id
            )
          },
          onBrushEnded: { startU, endU in
            activeBrush = nil
            let range = axis.inclusiveCalendarDayRange(
              fromNormalizedPosition: startU,
              toNormalizedPosition: endU
            )
            onBrushDateRange(range.earliestInclusive, range.latestInclusive, trawler.id)
          },
          onSelectMatch: onSelectMatch
        )
      }
    }
    .animation(reduceMotion ? nil : .easeInOut(duration: 0.18), value: selectedTrawlerID)
    .animation(reduceMotion ? nil : .easeInOut(duration: 0.18), value: isSearchActive)

    if let maxHeight = isSearchActive ? maxSearchLanesHeight : restingAvailableLanesHeight {
      let naturalHeight = totalNaturalLanesHeight(baseRestingHeight: baseRestingHeight)
      if naturalHeight > maxHeight {
        ScrollView(.vertical) {
          content
        }
        .frame(height: maxHeight)
      } else {
        content
      }
    } else {
      content
    }
  }

  private func totalNaturalLanesHeight(baseRestingHeight: CGFloat) -> CGFloat {
    trawlers.reduce(0) { sum, trawler in
      if isSearchActive {
        let isSlimmed = selectedTrawlerID != nil && selectedTrawlerID != trawler.id
        return sum + (isSlimmed ? TrawlDesign.strataSlimLaneHeight : TrawlDesign.strataLaneHeight)
      }
      return sum + baseRestingHeight
    }
  }

  private func axisBrushGesture(geometry: StrataTrackGeometry) -> some Gesture {
    DragGesture(minimumDistance: 6, coordinateSpace: .local)
      .onChanged { value in
        guard geometry.containsDatedX(value.startLocation.x),
          let startU = geometry.datedNormalizedPosition(forX: value.startLocation.x),
          let currentU = geometry.datedNormalizedPosition(forX: value.location.x)
        else {
          return
        }
        activeBrush = StrataDragBrush(
          startNormalizedX: startU,
          currentNormalizedX: currentU,
          targetTrawlerID: nil
        )
      }
      .onEnded { value in
        defer { activeBrush = nil }
        guard geometry.containsDatedX(value.startLocation.x),
          let startU = geometry.datedNormalizedPosition(forX: value.startLocation.x),
          let endU = geometry.datedNormalizedPosition(forX: value.location.x)
        else {
          return
        }
        let range = axis.inclusiveCalendarDayRange(
          fromNormalizedPosition: startU,
          toNormalizedPosition: endU
        )
        onBrushDateRange(range.earliestInclusive, range.latestInclusive, selectedTrawlerID)
      }
  }
}

private struct StrataTrackGeometry {
  let undatedMinX: CGFloat
  let undatedWidth: CGFloat
  let datedMinX: CGFloat
  let datedWidth: CGFloat
  let futureMinX: CGFloat
  let futureWidth: CGFloat

  var undatedCenterX: CGFloat { undatedMinX + undatedWidth / 2 }
  var datedMaxX: CGFloat { datedMinX + datedWidth }

  init(totalWidth: CGFloat, showsUndatedColumn: Bool) {
    let undatedW: CGFloat = showsUndatedColumn ? TrawlDesign.strataUndatedColumnWidth : 0
    let futureW: CGFloat = TrawlDesign.strataFutureGutterWidth
    let gap: CGFloat = 6
    self.undatedMinX = 0
    self.undatedWidth = undatedW
    self.datedMinX = undatedW > 0 ? undatedW + gap : 0
    let reservedRight = futureW + gap
    self.datedWidth = max(40, totalWidth - self.datedMinX - reservedRight)
    self.futureMinX = self.datedMinX + self.datedWidth + gap
    self.futureWidth = futureW
  }

  func containsDatedX(_ x: CGFloat) -> Bool {
    x >= datedMinX && x <= datedMaxX
  }

  func datedNormalizedPosition(forX x: CGFloat) -> Double? {
    guard datedWidth > 0 else { return nil }
    let clamped = min(max(x, datedMinX), datedMaxX)
    return Double((clamped - datedMinX) / datedWidth)
  }
}

private struct StrataLaneRow: View {
  let trawler: RestingTrawler
  let status: TrawlerStatus?
  let detailOverride: String?
  let accentColor: Color
  let axis: StrataTimeAxis
  let showsUndatedColumn: Bool
  let laneHeight: CGFloat
  let isRestingExpanded: Bool
  let isFocused: Bool
  let isSlimmed: Bool
  let isUpdating: Bool
  let isSearchActive: Bool
  let highlightedRange: ClosedRange<Double>?
  let isBrushTarget: Bool
  let laneMatches: [SearchMatch]
  let searchResult: TrawlerSearchResult?
  let searchFailed: Bool
  let searchSkipped: Bool
  let isExcludedByScope: Bool
  let selectedMatchID: SearchMatchIdentifier?
  let onToggleScope: () -> Void
  let onBrushChanged: (Double, Double) -> Void
  let onBrushEnded: (Double, Double) -> Void
  let onSelectMatch: (SearchMatch) -> Void

  var body: some View {
    HStack(spacing: 0) {
      Button(action: onToggleScope) {
        HStack(spacing: isSlimmed ? 5 : (isRestingExpanded ? 10 : 8)) {
          TrawlerIconView(
            registeredTrawler: trawler.id,
            size: isSlimmed ? 11 : (isRestingExpanded ? 22 : 17)
          )
          .opacity(trawler.state == .comingSoon ? 0.45 : (isSlimmed ? 0.65 : 1.0))
          Text(trawler.registeredTrawlerDisplayName)
            .font(
              isSlimmed
                ? .system(size: 10.5, weight: .regular)
                : (isFocused
                  ? .system(size: isRestingExpanded ? 13.5 : 12.5, weight: .semibold)
                  : .system(size: isRestingExpanded ? 13.5 : 12.5, weight: .medium))
            )
            .foregroundStyle(
              isFocused
                ? TrawlDesign.Colors.selectBlue
                : (trawler.state == .comingSoon || isSlimmed
                  ? TrawlDesign.Colors.mutedInk
                  : TrawlDesign.Colors.ink)
            )
            .lineLimit(1)
          Spacer(minLength: 4)
        }
        .padding(.leading, TrawlDesign.Space.md)
        .padding(.trailing, TrawlDesign.Space.sm)
        .frame(width: TrawlDesign.strataLabelColumnWidth, height: laneHeight, alignment: .leading)
        .contentShape(.rect)
      }
      .buttonStyle(.plain)
      .disabled(trawler.state == .comingSoon)
      .accessibilityLabel(laneAccessibilityLabel)
      .accessibilityAddTraits(isFocused ? .isSelected : [])

      GeometryReader { proxy in
        let geometry = StrataTrackGeometry(
          totalWidth: proxy.size.width,
          showsUndatedColumn: showsUndatedColumn
        )
        let placements = StrataMatchMarkerLayout.placements(
          for: laneMatches,
          axis: axis,
          undatedCenterX: Double(geometry.undatedCenterX),
          datedMinX: Double(geometry.datedMinX),
          datedWidth: Double(geometry.datedWidth),
          futureMinX: Double(geometry.futureMinX),
          futureWidth: Double(geometry.futureWidth)
        )
        Canvas { context, size in
          drawLane(
            in: &context,
            size: size,
            geometry: geometry,
            placements: placements
          )
        }
        .contentShape(.rect)
        .gesture(trackInteractionGesture(geometry: geometry, placements: placements))
      }
      .frame(height: laneHeight)

      countColumn
        .padding(.trailing, TrawlDesign.Space.md)
        .frame(width: TrawlDesign.strataCountColumnWidth, height: laneHeight, alignment: .trailing)
    }
    .frame(height: laneHeight)
    .background(laneBackground)
    .overlay(alignment: .leading) {
      if isFocused || isBrushTarget {
        Rectangle()
          .fill(TrawlDesign.Colors.selectBlue)
          .frame(width: 3)
      }
    }
    .overlay(alignment: .bottom) {
      Rectangle()
        .fill(TrawlDesign.Colors.hairline.opacity(0.65))
        .frame(height: 0.5)
    }
  }

  private var laneBackground: Color {
    if isFocused || isBrushTarget {
      return TrawlDesign.Colors.selectBlueSurface
    }
    return .clear
  }

  @ViewBuilder
  private var countColumn: some View {
    let readout = countReadout
    HStack(spacing: 4) {
      if readout.hasAttentionDot {
        Circle()
          .fill(TrawlDesign.Colors.warningAmber)
          .frame(width: 6, height: 6)
      }
      Text(readout.text)
        .font(
          isSlimmed
            ? .system(size: 10, weight: .regular, design: .monospaced)
            : .system(
              size: isRestingExpanded ? 12 : 11.5,
              weight: readout.emphasized ? .semibold : .regular,
              design: .monospaced
            )
        )
        .monospacedDigit()
        .foregroundStyle(readout.color)
        .lineLimit(1)
    }
    .help(readout.tooltip ?? "")
  }

  private struct CountReadout {
    let text: String
    let color: Color
    let emphasized: Bool
    var hasAttentionDot: Bool = false
    var tooltip: String? = nil
  }

  private var countReadout: CountReadout {
    if trawler.state == .comingSoon {
      return CountReadout(text: "Soon", color: TrawlDesign.Colors.mutedInk.opacity(0.7), emphasized: false)
    }
    if isUpdating {
      return CountReadout(text: "Updating…", color: TrawlDesign.Colors.selectBlue, emphasized: false)
    }
    if isSearchActive {
      if isExcludedByScope {
        return CountReadout(text: "·", color: TrawlDesign.Colors.mutedInk.opacity(0.5), emphasized: false)
      }
      if searchFailed {
        return CountReadout(text: "Failed", color: TrawlDesign.Colors.warningAmber, emphasized: true)
      }
      if searchSkipped {
        return CountReadout(text: "Skipped", color: TrawlDesign.Colors.warningAmber, emphasized: false)
      }
      if let searchResult {
        let countText =
          searchResult.totalSearchMatchesIsLowerBound
          ? "\(searchResult.totalSearchMatches.formatted())+"
          : searchResult.totalSearchMatches.formatted()
        if searchResult.totalSearchMatches > 0 {
          return CountReadout(text: countText, color: TrawlDesign.Colors.ink, emphasized: true)
        }
        return CountReadout(text: "0", color: TrawlDesign.Colors.mutedInk.opacity(0.75), emphasized: false)
      }
      return CountReadout(text: "0", color: TrawlDesign.Colors.mutedInk.opacity(0.6), emphasized: false)
    }

    // At rest, report the exact record total from the same ArchiveMonthlyHistory
    // that drives this lane's bars so every row uses one consistent unit.
    if let history = status?.archiveMonthlyHistory {
      let total = history.totalMeasuredRecordCount
      let hasWarning = trawler.state == .failed || trawler.state == .skipped || trawler.needsAttention
      return CountReadout(
        text: TrawlerRestingCopy.compactRecordCount(total),
        color: isSlimmed ? TrawlDesign.Colors.mutedInk : TrawlDesign.Colors.ink.opacity(0.88),
        emphasized: false,
        hasAttentionDot: hasWarning,
        tooltip: detailOverride ?? trawler.detail
      )
    }

    if let detailOverride {
      let isProblem = trawler.state == .failed || trawler.state == .skipped
      return CountReadout(
        text: isProblem ? "Unavailable" : detailOverride,
        color: isProblem ? TrawlDesign.Colors.warningAmber : TrawlDesign.Colors.mutedInk,
        emphasized: isProblem,
        tooltip: detailOverride
      )
    }
    return CountReadout(
      text: "Pending",
      color: TrawlDesign.Colors.mutedInk.opacity(0.75),
      emphasized: false,
      tooltip: "Archive history will populate on the next update."
    )
  }

  private var laneAccessibilityLabel: String {
    let base = "\(trawler.registeredTrawlerDisplayName), \(countReadout.text)"
    if trawler.state == .comingSoon {
      return "\(base). Coming soon."
    }
    if status?.archiveMonthlyHistory == nil {
      return "\(base). Monthly history will populate on next archive update."
    }
    return isFocused
      ? "\(base). Filtered to this app. Activate to clear filter."
      : "\(base). Activate to filter to this app."
  }

  private func drawLane(
    in context: inout GraphicsContext,
    size: CGSize,
    geometry: StrataTrackGeometry,
    placements: [StrataMatchMarkerPlacement]
  ) {
    let midY = size.height / 2
    let verticalMargin: CGFloat = isSlimmed ? 4 : (isRestingExpanded ? 12 : 8)
    let maxBarHeight = max(5, size.height - verticalMargin)

    // Brushed date band highlight across the dated track.
    if let range = highlightedRange {
      let x0 = geometry.datedMinX + CGFloat(range.lowerBound) * geometry.datedWidth
      let x1 = geometry.datedMinX + CGFloat(range.upperBound) * geometry.datedWidth
      let bandRect = CGRect(x: x0, y: 0, width: max(2, x1 - x0), height: size.height)
      context.fill(Path(bandRect), with: .color(TrawlDesign.Colors.selectBlueSurface))
      var leftEdge = Path()
      leftEdge.move(to: CGPoint(x: x0, y: 0))
      leftEdge.addLine(to: CGPoint(x: x0, y: size.height))
      var rightEdge = Path()
      rightEdge.move(to: CGPoint(x: x1, y: 0))
      rightEdge.addLine(to: CGPoint(x: x1, y: size.height))
      context.stroke(leftEdge, with: .color(TrawlDesign.Colors.selectBlue.opacity(0.65)), lineWidth: 1)
      context.stroke(rightEdge, with: .color(TrawlDesign.Colors.selectBlue.opacity(0.65)), lineWidth: 1)
    }

    // Subtle track separators (undated divider + Today divider).
    if showsUndatedColumn {
      var undatedSep = Path()
      let sepX = geometry.undatedWidth + 3
      undatedSep.move(to: CGPoint(x: sepX, y: 2))
      undatedSep.addLine(to: CGPoint(x: sepX, y: size.height - 2))
      context.stroke(undatedSep, with: .color(TrawlDesign.Colors.hairline), lineWidth: 0.75)
    }
    var todayLine = Path()
    let todayX = geometry.datedMaxX + 3
    todayLine.move(to: CGPoint(x: todayX, y: 1))
    todayLine.addLine(to: CGPoint(x: todayX, y: size.height - 1))
    context.stroke(todayLine, with: .color(TrawlDesign.Colors.hairline), lineWidth: 0.75)

    // Lane baseline.
    var baseline = Path()
    baseline.move(to: CGPoint(x: geometry.datedMinX, y: midY))
    baseline.addLine(to: CGPoint(x: geometry.datedMaxX, y: midY))

    guard let history = status?.archiveMonthlyHistory, trawler.state != .comingSoon else {
      // Unmeasured or coming-soon archive: draw a subtle dashed line, never flat zeros.
      context.stroke(
        baseline,
        with: .color(TrawlDesign.Colors.hairline),
        style: StrokeStyle(lineWidth: 1, dash: [3, 4])
      )
      return
    }

    context.stroke(baseline, with: .color(TrawlDesign.Colors.hairline.opacity(0.55)), lineWidth: 0.5)

    let scopeOpacityMultiplier: Double = isExcludedByScope ? 0.32 : 1.0

    // Undated record bar (left dedicated slot, e.g. Contacts).
    if showsUndatedColumn && history.undatedRecordCount > 0 {
      let heightFrac = StrataIntensityScale.barHeightFraction(forRecordCount: history.undatedRecordCount)
      let fillAlpha = StrataIntensityScale.fillOpacity(forRecordCount: history.undatedRecordCount)
      let barH = max(3, CGFloat(heightFrac) * maxBarHeight)
      let barW = min(14, geometry.undatedWidth - 6)
      let rect = CGRect(
        x: geometry.undatedCenterX - barW / 2,
        y: midY - barH / 2,
        width: barW,
        height: barH
      )
      let dateFilterDim: Double = highlightedRange != nil ? 0.25 : 1.0
      context.fill(
        Path(rect),
        with: .color(accentColor.opacity(fillAlpha * scopeOpacityMultiplier * dateFilterDim))
      )
    }

    // Dated and future monthly bars using the shared squared-log intensity scale.
    for bucket in history.monthlyRecordCountsInChronologicalOrder where bucket.recordCount > 0 {
      guard
        let monthStart = StrataTimeAxis.utcMonthStart(
          year: Int(bucket.calendarYear),
          month: Int(bucket.calendarMonth)
        ),
        let monthEnd = StrataTimeAxis.utcMonthEndExclusive(
          year: Int(bucket.calendarYear),
          month: Int(bucket.calendarMonth)
        )
      else {
        continue
      }

      let heightFrac = StrataIntensityScale.barHeightFraction(forRecordCount: bucket.recordCount)
      let fillAlpha = StrataIntensityScale.fillOpacity(forRecordCount: bucket.recordCount)
      let barH = max(3, CGFloat(heightFrac) * maxBarHeight)

      if axis.isFuture(monthStart) {
        let u0 = axis.futureNormalizedPosition(for: monthStart)
        let u1 = axis.futureNormalizedPosition(for: monthEnd)
        let x0 = geometry.futureMinX + CGFloat(u0) * geometry.futureWidth
        let x1 = geometry.futureMinX + CGFloat(u1) * geometry.futureWidth
        let barW = max(2.2, x1 - x0 - 0.6)
        let rect = CGRect(x: x0, y: midY - barH / 2, width: barW, height: barH)
        let dateFilterDim: Double = highlightedRange != nil ? 0.25 : 1.0
        context.fill(
          Path(rect),
          with: .color(accentColor.opacity(fillAlpha * scopeOpacityMultiplier * dateFilterDim))
        )
      } else {
        let clampedEnd = min(monthEnd, axis.referenceDate)
        let u0 = axis.datedNormalizedPosition(for: monthStart)
        let u1 = axis.datedNormalizedPosition(for: clampedEnd)
        let x0 = geometry.datedMinX + CGFloat(u0) * geometry.datedWidth
        let x1 = geometry.datedMinX + CGFloat(u1) * geometry.datedWidth
        let barW = max(2.0, x1 - x0 - 0.6)
        let rect = CGRect(x: x0, y: midY - barH / 2, width: barW, height: barH)

        var rangeDim: Double = 1.0
        if let highlightedRange {
          let overlaps = u1 >= highlightedRange.lowerBound && u0 <= highlightedRange.upperBound
          rangeDim = overlaps ? 1.0 : 0.22
        }
        context.fill(
          Path(rect),
          with: .color(accentColor.opacity(fillAlpha * scopeOpacityMultiplier * rangeDim))
        )
      }
    }

    // Active search match diamonds at exact horizontal date (stacked vertically if coincident).
    if isSearchActive && !placements.isEmpty {
      for placement in placements {
        let isSelected = placement.matchIdentifier == selectedMatchID
        let center = CGPoint(
          x: CGFloat(placement.exactX),
          y: midY + CGFloat(placement.verticalStackOffset)
        )
        let radius: CGFloat = isSelected ? 5.5 : (isSlimmed ? 3.2 : 4.2)
        let diamond = diamondPath(center: center, radius: radius)
        context.stroke(diamond, with: .color(TrawlDesign.Colors.paper), lineWidth: 2.0)
        context.fill(diamond, with: .color(TrawlDesign.Colors.matchRed))
        if isSelected {
          let ring = Path(
            ellipseIn: CGRect(
              x: center.x - 8,
              y: center.y - 8,
              width: 16,
              height: 16
            ))
          context.stroke(ring, with: .color(TrawlDesign.Colors.selectBlue), lineWidth: 1.5)
        }
      }
    }
  }

  private func diamondPath(center: CGPoint, radius: CGFloat) -> Path {
    var path = Path()
    path.move(to: CGPoint(x: center.x, y: center.y - radius))
    path.addLine(to: CGPoint(x: center.x + radius, y: center.y))
    path.addLine(to: CGPoint(x: center.x, y: center.y + radius))
    path.addLine(to: CGPoint(x: center.x - radius, y: center.y))
    path.closeSubpath()
    return path
  }

  private func trackInteractionGesture(
    geometry: StrataTrackGeometry,
    placements: [StrataMatchMarkerPlacement]
  ) -> some Gesture {
    DragGesture(minimumDistance: 0, coordinateSpace: .local)
      .onChanged { value in
        guard trawler.state != .comingSoon else { return }
        let dx = abs(value.translation.width)
        guard dx >= 8,
          geometry.containsDatedX(value.startLocation.x),
          let startU = geometry.datedNormalizedPosition(forX: value.startLocation.x),
          let currentU = geometry.datedNormalizedPosition(forX: value.location.x)
        else {
          return
        }
        onBrushChanged(startU, currentU)
      }
      .onEnded { value in
        guard trawler.state != .comingSoon else { return }
        let dx = abs(value.translation.width)
        if dx >= 8 {
          if geometry.containsDatedX(value.startLocation.x),
            let startU = geometry.datedNormalizedPosition(forX: value.startLocation.x),
            let endU = geometry.datedNormalizedPosition(forX: value.location.x)
          {
            onBrushEnded(startU, endU)
          }
          return
        }
        // Short click: check if the user clicked a match diamond first; otherwise toggle app scope.
        if isSearchActive,
          let nearest = nearestPlacement(
            to: value.location,
            in: placements,
            midY: laneHeight / 2,
            maxDistance: 10
          ),
          let match = laneMatches.first(where: { $0.id == nearest.matchIdentifier })
        {
          onSelectMatch(match)
          return
        }
        onToggleScope()
      }
  }

  private func nearestPlacement(
    to point: CGPoint,
    in placements: [StrataMatchMarkerPlacement],
    midY: CGFloat,
    maxDistance: CGFloat
  ) -> StrataMatchMarkerPlacement? {
    var best: StrataMatchMarkerPlacement?
    var bestDist = maxDistance
    for placement in placements {
      let dx = CGFloat(placement.exactX) - point.x
      let dy = (midY + CGFloat(placement.verticalStackOffset)) - point.y
      let dist = hypot(dx, dy)
      if dist <= bestDist {
        bestDist = dist
        best = placement
      }
    }
    return best
  }
}
