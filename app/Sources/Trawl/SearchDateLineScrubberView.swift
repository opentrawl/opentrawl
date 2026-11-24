import SwiftUI
import TrawlClient
import TrawlCore

/// Shared continuous line/area date scrubber anchored cleanly along the bottom of
/// the Search tab. Matches the Overview line grammar, never jumps or resizes, and
/// lets the user drag a ruby range band to filter by dates (or clear dates in one click).
struct SearchDateLineScrubberView: View {
  let trawlerStatuses: [TrawlerStatus]
  let scopedTrawlerID: RegisteredTrawlerIdentity?
  let queryText: String
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

  private var timeBounds: (start: Date, end: Date) {
    let now = Date()
    let histories = relevantStatuses.compactMap(\.archiveWeeklyHistory)
    let axis = ArchiveTimelineTimeAxis.build(from: histories, referenceDate: now)
    return (axis.earliestAxisDate, now)
  }

  private var yearLabels: [String] {
    let bounds = timeBounds
    let calendar = Calendar(identifier: .gregorian)
    let startYear = calendar.component(.year, from: bounds.start)
    let endYear = calendar.component(.year, from: bounds.end)
    guard endYear > startYear else { return ["\(endYear)"] }
    let span = endYear - startYear
    let step = max(1, Int(ceil(Double(span) / 8.0)))
    var out: [String] = []
    var year = startYear
    while year <= endYear {
      out.append("\(year)")
      year += step
    }
    if out.last != "\(endYear)" {
      out.append("\(endYear)")
    }
    return out
  }

  private var activeFractions: (lower: CGFloat, upper: CGFloat)? {
    if let draftStartFraction, let draftEndFraction {
      return (min(draftStartFraction, draftEndFraction), max(draftStartFraction, draftEndFraction))
    }
    guard earliestDateInclusive != nil || latestDateInclusive != nil else { return nil }
    let bounds = timeBounds
    let span = max(bounds.end.timeIntervalSince(bounds.start), 1)
    let lower = earliestDateInclusive.map {
      CGFloat(min(max($0.timeIntervalSince(bounds.start) / span, 0), 1))
    } ?? 0
    let upper = latestDateInclusive.map {
      CGFloat(min(max($0.timeIntervalSince(bounds.start) / span, 0), 1))
    } ?? 1
    return (min(lower, upper), max(lower, upper))
  }

  private var rangeBadgeText: String? {
    guard let fractions = activeFractions else { return nil }
    let bounds = timeBounds
    let span = max(bounds.end.timeIntervalSince(bounds.start), 1)
    let d1 = bounds.start.addingTimeInterval(TimeInterval(fractions.lower) * span)
    let d2 = bounds.start.addingTimeInterval(TimeInterval(fractions.upper) * span)
    let rangeStr =
      "\(d1.formatted(.dateTime.month(.abbreviated).year())) – \(d2.formatted(.dateTime.month(.abbreviated).year()))"
    return resultSummaryText.isEmpty ? rangeStr : "\(rangeStr) · \(resultSummaryText)"
  }

  var body: some View {
    VStack(spacing: 5) {
      HStack(spacing: 8) {
        Text("When")
          .font(.system(size: 11.5, weight: .semibold))
          .foregroundStyle(TrawlDesign.Colors.ink)
        Text(
          queryText.isEmpty
            ? "Drag across the line to choose dates"
            : "Results for “\(queryText)” · Drag across the line to choose dates"
        )
        .font(.system(size: 11.5))
        .foregroundStyle(TrawlDesign.Colors.mutedInk)
        .lineLimit(1)
        Spacer(minLength: 8)
        if let rangeBadgeText {
          Text(rangeBadgeText)
            .font(.system(size: 11.5, weight: .semibold))
            .foregroundStyle(TrawlDesign.Colors.matchRed)
            .lineLimit(1)
          Button("Clear dates", action: onClearDateRange)
            .buttonStyle(.bordered)
            .controlSize(.mini)
        }
      }

      GeometryReader { proxy in
        let width = max(proxy.size.width, 1)
        let height = proxy.size.height

        ZStack(alignment: .leading) {
          Canvas { context, size in
            drawScrubberCurve(in: &context, size: size)
          }

          if let active = activeFractions {
            let x1 = active.lower * width
            let x2 = active.upper * width
            let bandW = max(4, x2 - x1)
            Rectangle()
              .fill(TrawlDesign.Colors.matchRed.opacity(0.12))
              .frame(width: bandW, height: height)
              .offset(x: x1)

            // Left handle
            Capsule()
              .fill(TrawlDesign.Colors.matchRed)
              .frame(width: 5, height: 16)
              .offset(x: max(0, x1 - 2.5))
            // Right handle
            Capsule()
              .fill(TrawlDesign.Colors.matchRed)
              .frame(width: 5, height: 16)
              .offset(x: min(width - 5, x2 - 2.5))
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
              let bounds = timeBounds
              let span = max(bounds.end.timeIntervalSince(bounds.start), 1)
              let lower = min(s, e)
              let upper = max(s, e)
              let d1 = bounds.start.addingTimeInterval(TimeInterval(lower) * span)
              let d2 = bounds.start.addingTimeInterval(
                TimeInterval(max(upper, min(1, lower + 0.01))) * span
              )
              onCommitDateRange(d1, d2)
            }
        )
      }
      .frame(height: 34)

      HStack {
        ForEach(Array(yearLabels.enumerated()), id: \.offset) { index, label in
          if index > 0 { Spacer() }
          Text(label)
            .font(TrawlDesign.Typography.monoAxis)
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
      }
    }
    .padding(.horizontal, 16)
    .padding(.vertical, 9)
    .frame(height: 88)
    .background(TrawlDesign.Colors.surface.opacity(0.85))
    .overlay(alignment: .top) {
      Divider()
    }
  }

  private func drawScrubberCurve(in context: inout GraphicsContext, size: CGSize) {
    let baselineY = size.height - 2
    let points = aggregatedWeeklyCounts
    guard !points.isEmpty else {
      var line = Path()
      line.move(to: CGPoint(x: 0, y: baselineY))
      line.addLine(to: CGPoint(x: size.width, y: baselineY))
      context.stroke(line, with: .color(TrawlDesign.Colors.hairline), lineWidth: 1)
      return
    }

    let bounds = timeBounds
    let visiblePoints = points.filter {
      $0.weekStart >= bounds.start && $0.weekStart <= bounds.end
    }
    guard !visiblePoints.isEmpty else { return }
    let maxVal = Double(visiblePoints.map(\.recordCount).max() ?? 0)
    guard maxVal > 0 else { return }

    let span = max(bounds.end.timeIntervalSince(bounds.start), 1)
    let usableHeight = max(size.height - 6, 4)

    var strokePath = Path()
    var fillPath = Path()
    var previousWeekStart: Date?
    var previousX: CGFloat = 0
    var hasStarted = false

    for item in visiblePoints {
      let frac = min(max(item.weekStart.timeIntervalSince(bounds.start) / span, 0), 1)
      let x = CGFloat(frac) * size.width
      let y = item.recordCount > 0
        ? baselineY - max(1.5, CGFloat(Double(item.recordCount) / maxVal) * usableHeight)
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
        width: max(4, (active.upper - active.lower) * size.width),
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
