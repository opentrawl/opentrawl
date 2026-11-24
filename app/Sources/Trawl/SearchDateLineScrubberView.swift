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

  private var activeFractions: (lower: CGFloat, upper: CGFloat)? {
    if let draftStartFraction, let draftEndFraction {
      return (min(draftStartFraction, draftEndFraction), max(draftStartFraction, draftEndFraction))
    }
    guard earliestDateInclusive != nil || latestDateInclusive != nil else { return nil }
    let axis = timeAxis
    let lower = earliestDateInclusive.map { CGFloat(axis.datedNormalizedPosition(for: $0)) } ?? 0
    let upper = latestDateInclusive.map { CGFloat(axis.datedNormalizedPosition(for: $0)) } ?? 1
    return (min(lower, upper), max(lower, upper))
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
          .font(.subheadline.weight(.semibold))
          .foregroundStyle(.primary)
        Text("Drag across the line to choose dates.")
          .font(.caption)
          .foregroundStyle(.secondary)
          .lineLimit(1)
        Spacer(minLength: TrawlDesign.Space.sm)
        if let rangeBadgeText {
          Text(rangeBadgeText)
            .font(.caption.monospacedDigit())
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
              .fill(TrawlDesign.Colors.matchRed.opacity(0.14))
              .frame(width: bandW, height: height)
              .offset(x: x1)
          }
        }
        .contentShape(Rectangle())
        .gesture(
          DragGesture(minimumDistance: 2)
            .onChanged { value in
              let s = min(max(value.startLocation.x / width, 0), 1)
              let c = min(max(value.location.x / width, 0), 1)
              draftStartFraction = s
              draftEndFraction = c
            }
            .onEnded { value in
              let s = min(max(value.startLocation.x / width, 0), 1)
              let e = min(max(value.location.x / width, 0), 1)
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
      .frame(height: 34)

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
      .frame(height: 16)
    }
    .padding(.horizontal, TrawlDesign.Space.md)
    .padding(.vertical, TrawlDesign.Space.sm)
    .frame(height: 92)
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

    context.fill(fillPath, with: .color(TrawlDesign.Colors.mutedInk.opacity(0.15)))
    context.stroke(
      strokePath,
      with: .color(TrawlDesign.Colors.mutedInk.opacity(0.65)),
      style: StrokeStyle(lineWidth: 1.35, lineJoin: .round)
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
        layer.fill(fillPath, with: .color(TrawlDesign.Colors.matchRed.opacity(0.24)))
        layer.stroke(
          strokePath,
          with: .color(TrawlDesign.Colors.matchRed),
          style: StrokeStyle(lineWidth: 1.8, lineJoin: .round)
        )
      }
    }
  }
}
