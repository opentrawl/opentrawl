import SwiftUI
import TrawlClient
import TrawlCore

/// Shared continuous line/area date scrubber anchored cleanly along the bottom of
/// the Search tab. Uses the exact same sqrt-recency time axis (`ArchiveTimelineTimeAxis`)
/// and power-law vertical intensity (`ArchiveTimelineIntensityScale`) as Overview.
struct SearchDateLineScrubberView: View {
  let trawlerStatuses: [TrawlerStatus]
  let scopedTrawlerID: RegisteredTrawlerIdentity?
  let scopedTrawlerDisplayName: String?
  let resultSummaryText: String
  let earliestDateInclusive: Date?
  let latestDateInclusive: Date?
  let onCommitDateRange: (Date, Date) -> Void
  let onClearDateRange: () -> Void

  @State private var draftStartFraction: CGFloat?
  @State private var draftEndFraction: CGFloat?
  @State private var dragAnchorFraction: CGFloat?

  private static let brushHandleGrabDistance: CGFloat = 8

  private var relevantStatuses: [TrawlerStatus] {
    if let scopedTrawlerID {
      return trawlerStatuses.filter { $0.id == scopedTrawlerID }
    }
    return trawlerStatuses.filter { $0.id.registeredTrawlerIdentity != "contacts" }
  }

  private var timeAxis: ArchiveTimelineTimeAxis {
    let histories = relevantStatuses.compactMap(\.archiveWeeklyHistory)
    return ArchiveTimelineTimeAxis.build(from: histories, referenceDate: Date())
  }

  private var aggregatedWeeklyCounts: [(weekStart: Date, recordCount: UInt64)] {
    var totals: [Int64: UInt64] = [:]
    for status in relevantStatuses {
      guard let buckets = status.archiveWeeklyHistory?.weeklyRecordCountsInChronologicalOrder else {
        continue
      }
      for bucket in buckets {
        totals[bucket.utcWeekStartUnixSeconds, default: 0] += bucket.recordCount
      }
    }
    return totals.keys.sorted().map { unix in
      (weekStart: Date(timeIntervalSince1970: TimeInterval(unix)), recordCount: totals[unix] ?? 0)
    }
  }

  private var isDragging: Bool {
    draftStartFraction != nil && draftEndFraction != nil
  }

  private var committedFractions: (lower: CGFloat, upper: CGFloat)? {
    guard earliestDateInclusive != nil || latestDateInclusive != nil else { return nil }
    let axis = timeAxis
    let lower = earliestDateInclusive.map { CGFloat(axis.datedNormalizedPosition(for: $0)) } ?? 0
    let upper = latestDateInclusive.map { CGFloat(axis.datedNormalizedPosition(for: $0)) } ?? 1
    return (min(lower, upper), max(lower, upper))
  }

  private var activeFractions: (lower: CGFloat, upper: CGFloat)? {
    if let draftStartFraction, let draftEndFraction {
      return (min(draftStartFraction, draftEndFraction), max(draftStartFraction, draftEndFraction))
    }
    return committedFractions
  }

  /// A drag that starts on a handle moves that edge and keeps the other one.
  /// A drag that starts anywhere else picks a new range.
  private func anchorFraction(forDragStartingAt startX: CGFloat, width: CGFloat) -> CGFloat {
    let startFraction = min(max(startX / width, 0), 1)
    guard let committedFractions else { return startFraction }
    if abs(startX - committedFractions.lower * width) <= Self.brushHandleGrabDistance {
      return committedFractions.upper
    }
    if abs(startX - committedFractions.upper * width) <= Self.brushHandleGrabDistance {
      return committedFractions.lower
    }
    return startFraction
  }

  private var rangeBadgeText: String? {
    guard let fractions = activeFractions else { return nil }
    let axis = timeAxis
    let range = axis.inclusiveCalendarDayRange(
      fromNormalizedPosition: Double(fractions.lower),
      toNormalizedPosition: Double(fractions.upper)
    )
    let rangeStr =
      ArchiveTimelineDateRangeFormatting.label(
        earliestInclusive: range.earliestInclusive,
        latestInclusive: range.latestInclusive
      ) ?? ""
    if isDragging || resultSummaryText.isEmpty {
      return rangeStr
    }
    return "\(rangeStr) · \(resultSummaryText)"
  }

  var body: some View {
    let axis = timeAxis

    VStack(spacing: TrawlDesign.Space.xs) {
      HStack(spacing: TrawlDesign.Space.sm) {
        Text(scopedTrawlerDisplayName.map { "\($0) by week" } ?? "All items by week")
          .font(TrawlDesign.Typography.bodyEmphasis)
          .foregroundStyle(.primary)
        Text("Drag across the line to choose dates.")
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
          .lineLimit(1)
        Spacer(minLength: TrawlDesign.Space.sm)
        if let rangeBadgeText {
          Text(rangeBadgeText)
            .font(TrawlDesign.Typography.monoSmall.weight(.semibold))
            .foregroundStyle(TrawlDesign.Colors.matchRed)
            .lineLimit(1)
          if !isDragging {
            Button("Clear dates", action: onClearDateRange)
              .buttonStyle(.bordered)
              .controlSize(.small)
          }
        }
      }

      GeometryReader { proxy in
        let width = max(proxy.size.width, 1)
        let height = proxy.size.height

        ZStack(alignment: .leading) {
          Canvas { context, size in
            drawScrubberCurve(in: &context, size: size, axis: axis)
          }

          if let active = activeFractions {
            let x1 = active.lower * width
            let x2 = active.upper * width
            let bandW = max(3, x2 - x1)
            Rectangle()
              .fill(TrawlDesign.Colors.matchRed.opacity(0.12))
              .frame(width: bandW, height: height)
              .offset(x: x1)
            DateBrushEdgeHandle(height: height)
              .offset(x: x1 - DateBrushEdgeHandle.width / 2)
            DateBrushEdgeHandle(height: height)
              .offset(x: x1 + bandW - DateBrushEdgeHandle.width / 2)
          }
        }
        .contentShape(Rectangle())
        .gesture(
          DragGesture(minimumDistance: 2)
            .onChanged { value in
              let anchor =
                dragAnchorFraction
                ?? anchorFraction(forDragStartingAt: value.startLocation.x, width: width)
              dragAnchorFraction = anchor
              draftStartFraction = anchor
              draftEndFraction = min(max(value.location.x / width, 0), 1)
            }
            .onEnded { value in
              let s =
                dragAnchorFraction
                ?? anchorFraction(forDragStartingAt: value.startLocation.x, width: width)
              let e = min(max(value.location.x / width, 0), 1)
              dragAnchorFraction = nil
              draftStartFraction = nil
              draftEndFraction = nil
              let lower = min(s, e)
              let upper = max(s, e)
              let range = axis.inclusiveCalendarDayRange(
                fromNormalizedPosition: Double(lower),
                toNormalizedPosition: Double(upper)
              )
              onCommitDateRange(range.earliestInclusive, range.latestInclusive)
            }
        )
      }
      .frame(height: TrawlDesign.searchDateScrubberLineHeight)

      GeometryReader { proxy in
        let ticks = axis.yearTicks(forTrackWidth: Double(proxy.size.width))
        let pickedFractions = activeFractions
        ZStack(alignment: .leading) {
          ForEach(ticks) { tick in
            let x = CGFloat(tick.normalizedPosition) * proxy.size.width
            let isInPickedRange =
              pickedFractions.map {
                CGFloat(tick.normalizedPosition) >= $0.lower
                  && CGFloat(tick.normalizedPosition) <= $0.upper
              } ?? false
            Text(tick.label)
              .font(TrawlDesign.Typography.smallNumber)
              .fontWeight(isInPickedRange || tick.isToday ? .semibold : .regular)
              .foregroundStyle(
                isInPickedRange
                  ? TrawlDesign.Colors.matchRed
                  : (tick.isToday ? TrawlDesign.Colors.ink : TrawlDesign.Colors.mutedInk))
              .fixedSize()
              .position(
                x: min(max(x, 16), max(16, proxy.size.width - 18)),
                y: proxy.size.height / 2
              )
          }
        }
      }
      .frame(height: 16)
    }
    .padding(.horizontal, TrawlDesign.Space.md)
    .padding(.vertical, TrawlDesign.Space.sm)
    .frame(height: 108)
    .background(TrawlDesign.Colors.surface)
    .overlay(alignment: .top) {
      Divider()
    }
  }

  private func drawScrubberCurve(
    in context: inout GraphicsContext,
    size: CGSize,
    axis: ArchiveTimelineTimeAxis
  ) {
    let baselineY = size.height - 2
    let visiblePoints = aggregatedWeeklyCounts.filter {
      $0.weekStart >= axis.earliestAxisDate && $0.weekStart <= axis.referenceDate
    }
    guard !visiblePoints.isEmpty else {
      var line = Path()
      line.move(to: CGPoint(x: 0, y: baselineY))
      line.addLine(to: CGPoint(x: size.width, y: baselineY))
      context.stroke(line, with: .color(TrawlDesign.Colors.hairline), lineWidth: 1)
      return
    }

    let rowMax = visiblePoints.map(\.recordCount).max() ?? 0
    guard rowMax > 0 else { return }

    let usableHeight = max(size.height - 5, 4)

    var strokePath = Path()
    var fillPath = Path()
    var previousWeekStart: Date?
    var previousX: CGFloat = 0
    var hasStarted = false

    for item in visiblePoints {
      let x = CGFloat(axis.datedNormalizedPosition(for: item.weekStart)) * size.width
      let heightFraction = ArchiveTimelineIntensityScale.heightFraction(
        forRecordCount: item.recordCount,
        rowMaximumRecordCount: rowMax
      )
      let y = item.recordCount > 0
        ? baselineY - max(1.5, CGFloat(heightFraction) * usableHeight)
        : baselineY
      let pt = CGPoint(x: x, y: y)
      if !hasStarted {
        hasStarted = true
        strokePath.move(to: pt)
        fillPath.move(to: CGPoint(x: x, y: baselineY))
        fillPath.addLine(to: pt)
      } else if let prev = previousWeekStart,
        item.weekStart.timeIntervalSince(prev) > 21 * 86_400
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
      previousWeekStart = item.weekStart
      previousX = x
    }

    guard hasStarted else { return }
    fillPath.addLine(to: CGPoint(x: previousX, y: baselineY))
    fillPath.closeSubpath()

    context.fill(fillPath, with: .color(TrawlDesign.Colors.mutedInk.opacity(0.18)))
    context.stroke(
      strokePath,
      with: .color(TrawlDesign.Colors.mutedInk.opacity(0.70)),
      style: StrokeStyle(lineWidth: 1.4, lineJoin: .round)
    )

    if let active = activeFractions {
      let clipRect = CGRect(
        x: active.lower * size.width,
        y: 0,
        width: max(3, (active.upper - active.lower) * size.width),
        height: size.height
      )
      context.drawLayer { layer in
        layer.clip(to: Path(clipRect))
        layer.fill(fillPath, with: .color(TrawlDesign.Colors.matchRed.opacity(0.28)))
        layer.stroke(
          strokePath,
          with: .color(TrawlDesign.Colors.matchRed),
          style: StrokeStyle(lineWidth: 1.8, lineJoin: .round)
        )
      }
    }
  }
}

/// One edge of the picked date range: a red rule with a grip to drag.
private struct DateBrushEdgeHandle: View {
  static let width: CGFloat = 5

  let height: CGFloat

  var body: some View {
    ZStack {
      Rectangle()
        .fill(TrawlDesign.Colors.matchRed)
        .frame(width: 1.5, height: height)
      RoundedRectangle(cornerRadius: Self.width / 2, style: .continuous)
        .fill(TrawlDesign.Colors.matchRed)
        .overlay(
          RoundedRectangle(cornerRadius: Self.width / 2, style: .continuous)
            .strokeBorder(TrawlDesign.Colors.surface, lineWidth: 1)
        )
        .frame(width: Self.width, height: 16)
    }
    .frame(width: Self.width, height: height)
  }
}
