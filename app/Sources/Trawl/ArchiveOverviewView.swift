import SwiftUI
import TrawlClient
import TrawlCore

/// Overview (Home) view:
/// 1. Top: "Your archive" actionable constellation connecting your apps -> archive host -> AI & Other Macs.
/// 2. Bottom: "Items per week" fixed-height linear weekly curves (never resizing or jumping),
///    with live hover readout, keyboard date picker, and 1Y / 5Y / All zoom.
struct ArchiveOverviewView: View {
  @Environment(TrawlerIconStore.self) private var iconStore

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
  @State private var isShowingDatePickerPopover = false
  @State private var customStartDate = Calendar.current.date(byAdding: .year, value: -1, to: Date()) ?? Date()
  @State private var customEndDate = Date()

  private var datedTrawlers: [RestingTrawler] {
    trawlers.filter { $0.id.registeredTrawlerIdentity != "contacts" }
  }

  private var hasUndatedContacts: Bool {
    trawlers.contains { $0.id.registeredTrawlerIdentity == "contacts" }
  }

  var body: some View {
    GeometryReader { outerProxy in
      let isWideLayout = outerProxy.size.width >= 860
      ScrollView {
        VStack(alignment: .leading, spacing: 14) {
          constellationCard(isWideLayout: isWideLayout)
          weeklyLinesCard
        }
        .padding(16)
        .frame(width: outerProxy.size.width, alignment: .topLeading)
      }
      .background(TrawlDesign.Colors.surface.opacity(0.45))
    }
  }

  // MARK: - Top: Your archive constellation

  private func constellationCard(isWideLayout: Bool) -> some View {
    VStack(alignment: .leading, spacing: 10) {
      Text("Your archive")
        .font(.system(size: 14, weight: .semibold))
        .foregroundStyle(TrawlDesign.Colors.ink)

      if isWideLayout {
        wideConstellation
      } else {
        compactConstellation
      }
    }
    .padding(14)
    .background(
      TrawlDesign.Colors.paper,
      in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface, style: .continuous)
        .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
    )
  }

  private var wideConstellation: some View {
    HStack(alignment: .center, spacing: 18) {
      // Left: Your apps in two tidy columns
      appColumnsGrid
        .frame(maxWidth: .infinity, alignment: .leading)

      // Center: Host hub
      centerHostHub
        .frame(width: 170)

      // Right: Three honest actionable cards
      rightConnectionCards
        .frame(width: 308)
    }
    .frame(maxWidth: .infinity)
    .background {
      GeometryReader { proxy in
        ConstellationConnectingLines(size: proxy.size)
      }
      .allowsHitTesting(false)
    }
  }

  private var compactConstellation: some View {
    VStack(alignment: .leading, spacing: 14) {
      HStack(alignment: .top, spacing: 14) {
        centerHostHub
          .frame(width: 176)
        rightConnectionCards
      }
      appColumnsGrid
    }
  }

  private var appColumnsGrid: some View {
    let split = (trawlers.count + 1) / 2
    let leftColumn = Array(trawlers.prefix(split))
    let rightColumn = Array(trawlers.dropFirst(split))

    return HStack(alignment: .top, spacing: 10) {
      VStack(spacing: 8) {
        ForEach(leftColumn) { trawler in
          constellationAppNode(for: trawler)
        }
      }
      if !rightColumn.isEmpty {
        VStack(spacing: 8) {
          ForEach(rightColumn) { trawler in
            constellationAppNode(for: trawler)
          }
        }
      }
    }
  }

  private func constellationAppNode(for trawler: RestingTrawler) -> some View {
    let operability = operabilityByID[trawler.id]
    let count = operability?.archivedItemCount ?? 0
    let needsAttention = operability?.needsAttention == true
    let inlineActionTitle: String? = {
      guard needsAttention, let action = operability?.action else { return nil }
      switch action {
      case .signInGmail, .signInX:
        return "Sign in"
      case .openSourceApp:
        return "Open"
      case .openFullDiskAccessSettings, .openPrivacySettings, .retryUpdate:
        return nil
      }
    }()

    return HStack(spacing: 6) {
      Button {
        onBrowseApp(trawler.id)
      } label: {
        HStack(spacing: 7) {
          TrawlerIconView(registeredTrawler: trawler.id, size: 17)
          Text(trawler.registeredTrawlerDisplayName)
            .font(.system(size: 12, weight: .semibold))
            .foregroundStyle(TrawlDesign.Colors.ink)
            .lineLimit(1)
            .layoutPriority(1)
          Spacer(minLength: 4)
          if needsAttention && inlineActionTitle == nil {
            Circle()
              .fill(TrawlDesign.Colors.warningAmber)
              .frame(width: 6, height: 6)
          }
          Text(count.formatted())
            .font(TrawlDesign.Typography.monoSmall)
            .monospacedDigit()
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
            .lineLimit(1)
            .fixedSize(horizontal: true, vertical: false)
        }
        .contentShape(Rectangle())
      }
      .buttonStyle(.plain)

      if let inlineActionTitle, let action = operability?.action {
        Button(inlineActionTitle) {
          onRunAttentionAction(trawler.id, action)
        }
        .buttonStyle(.plain)
        .font(.system(size: 10, weight: .semibold))
        .foregroundStyle(TrawlDesign.Colors.warningAmber)
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .background(
          TrawlDesign.Colors.warningAmber.opacity(0.14),
          in: RoundedRectangle(cornerRadius: 4, style: .continuous)
        )
        .fixedSize()
      }
    }
    .padding(.horizontal, 9)
    .frame(maxWidth: .infinity, minHeight: 32, maxHeight: 32, alignment: .leading)
    .background(
      needsAttention ? TrawlDesign.Colors.warningAmber.opacity(0.04) : TrawlDesign.Colors.paper,
      in: RoundedRectangle(cornerRadius: 8, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: 8, style: .continuous)
        .stroke(
          needsAttention
            ? TrawlDesign.Colors.warningAmber.opacity(0.40)
            : TrawlDesign.Colors.hairline,
          lineWidth: 1
        )
    )
    .help(operability?.statusLine ?? trawler.registeredTrawlerDisplayName)
  }

  private var centerHostHub: some View {
    Button(action: onOpenHostSwitcher) {
      VStack(spacing: 5) {
        OpenTrawlMarkIcon(size: 28)
        Text(hostDisplayName)
          .font(.system(size: 13.5, weight: .bold))
          .foregroundStyle(TrawlDesign.Colors.ink)
          .lineLimit(1)
        Text("\(totalItemCount.formatted()) items")
          .font(.system(size: 12.5, weight: .semibold, design: .monospaced))
          .monospacedDigit()
          .foregroundStyle(TrawlDesign.Colors.ink)
        Text(freshnessLine)
          .font(.system(size: 11))
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
          .lineLimit(1)
      }
      .padding(.vertical, 16)
      .padding(.horizontal, 14)
      .frame(maxWidth: .infinity)
      .background(
        TrawlDesign.Colors.paper,
        in: RoundedRectangle(cornerRadius: 16, style: .continuous)
      )
      .overlay(
        RoundedRectangle(cornerRadius: 16, style: .continuous)
          .stroke(TrawlDesign.Colors.hairline, lineWidth: 1.5)
      )
      .shadow(color: .black.opacity(0.04), radius: 10, y: 3)
    }
    .buttonStyle(.plain)
    .help("Switch archive host")
  }

  private var rightConnectionCards: some View {
    VStack(spacing: 10) {
      if !isRemoteArchive {
        connectionCard(
          title: "AI apps on this Mac",
          subtitle: "Claude Code, Codex, Cursor and others",
          buttonTitle: hasCopiedAIPrompt ? "Copied" : HumanCopy.ConnectAI.copyAction,
          isProminent: true,
          action: onCopyAIPrompt
        )
        connectionCard(
          title: "AI apps on the web or phone",
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
  }

  private func connectionCard(
    title: String,
    subtitle: String,
    buttonTitle: String,
    isProminent: Bool,
    action: @escaping () -> Void
  ) -> some View {
    VStack(alignment: .leading, spacing: 6) {
      HStack(alignment: .center, spacing: 8) {
        Text(title)
          .font(.system(size: 12.5, weight: .semibold))
          .foregroundStyle(TrawlDesign.Colors.ink)
          .lineLimit(1)
        Spacer(minLength: 4)
        Button(buttonTitle, action: action)
          .buttonStyle(.plain)
          .font(.system(size: 11, weight: .semibold))
          .foregroundStyle(isProminent ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.ink)
          .padding(.horizontal, 9)
          .padding(.vertical, 4)
          .background(
            isProminent ? TrawlDesign.Colors.selectBlueSurface : TrawlDesign.Colors.surface,
            in: RoundedRectangle(cornerRadius: 6, style: .continuous)
          )
          .fixedSize()
      }
      Text(subtitle)
        .font(.system(size: 11.5))
        .foregroundStyle(TrawlDesign.Colors.mutedInk)
        .fixedSize(horizontal: false, vertical: true)
    }
    .padding(12)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(
      TrawlDesign.Colors.paper,
      in: RoundedRectangle(cornerRadius: 11, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: 11, style: .continuous)
        .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
    )
  }

  // MARK: - Bottom: Original Per-App Linear Weekly Lines (Fixed Row Height)

  private var timelineWindowBounds: (start: Date, end: Date) {
    let now = Date()
    let calendar = Calendar(identifier: .gregorian)
    switch chartSpan {
    case .oneYear:
      let start = calendar.date(byAdding: .year, value: -1, to: now) ?? now.addingTimeInterval(-365 * 86_400)
      return (start, now)
    case .fiveYears:
      let start = calendar.date(byAdding: .year, value: -5, to: now) ?? now.addingTimeInterval(-5 * 365 * 86_400)
      return (start, now)
    case .all:
      let histories = datedTrawlers.compactMap { statusesByID[$0.id]?.archiveWeeklyHistory }
      let axis = ArchiveTimelineTimeAxis.build(from: histories, referenceDate: now)
      return (axis.earliestAxisDate, now)
    }
  }

  private var axisYearLabels: [String] {
    let bounds = timelineWindowBounds
    let calendar = Calendar(identifier: .gregorian)
    let startYear = calendar.component(.year, from: bounds.start)
    let endYear = calendar.component(.year, from: bounds.end)
    guard endYear > startYear else { return ["\(endYear)"] }
    let span = endYear - startYear
    let step = max(1, Int(ceil(Double(span) / 8.0)))
    var years: [String] = []
    var year = startYear
    while year <= endYear {
      years.append("\(year)")
      year += step
    }
    if years.last != "\(endYear)" {
      years.append("\(endYear)")
    }
    return years
  }

  private var weeklyLinesCard: some View {
    let bounds = timelineWindowBounds

    return VStack(spacing: 0) {
      HStack(spacing: 10) {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
          Text("Items per week")
            .font(.system(size: 14, weight: .semibold))
            .foregroundStyle(TrawlDesign.Colors.ink)
          Text("Drag across a row to pick dates.")
            .font(.system(size: 11.5))
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
        Spacer(minLength: 8)
        if let hovered = hoveredWeek {
          Button {
            let weekEnd = hovered.weekStart.addingTimeInterval(7 * 86_400 - 1)
            onSelectDateRange(hovered.trawlerID, hovered.weekStart, weekEnd)
          } label: {
            HStack(spacing: 6) {
              Text("\(hovered.trawlerName) · week of \(hovered.weekStart.formatted(.dateTime.day().month(.abbreviated).year()))")
                .font(.system(size: 11.5, weight: .semibold))
              Text("\(hovered.count.formatted()) items")
                .font(TrawlDesign.Typography.monoSmall)
              Text("Show →")
                .font(.system(size: 11, weight: .semibold))
            }
            .foregroundStyle(TrawlDesign.Colors.matchRed)
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .background(
              TrawlDesign.Colors.matchRed.opacity(0.10),
              in: RoundedRectangle(cornerRadius: 7, style: .continuous)
            )
          }
          .buttonStyle(.plain)
        }

        Button("Choose dates…") {
          isShowingDatePickerPopover.toggle()
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .popover(isPresented: $isShowingDatePickerPopover, arrowEdge: .bottom) {
          VStack(alignment: .leading, spacing: 12) {
            Text("Choose dates")
              .font(.headline)
            DatePicker("From", selection: $customStartDate, displayedComponents: .date)
            DatePicker("To", selection: $customEndDate, displayedComponents: .date)
            HStack {
              Spacer()
              Button("Cancel") { isShowingDatePickerPopover = false }
              Button("Show items") {
                let start = min(customStartDate, customEndDate)
                let end = max(customStartDate, customEndDate)
                isShowingDatePickerPopover = false
                onSelectDateRange(nil, start, end)
              }
              .buttonStyle(.borderedProminent)
            }
          }
          .padding(14)
          .frame(width: 280)
        }

        Picker("Time range", selection: $chartSpan) {
          ForEach(OverviewChartSpan.allCases) { span in
            Text(span.rawValue).tag(span)
          }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .frame(width: 116)
      }
      .padding(.horizontal, 16)
      .padding(.vertical, 11)

      Divider()

      // Axis row
      HStack(spacing: 12) {
        Text("App")
          .font(.system(size: 11, weight: .semibold))
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
          .frame(width: 124, alignment: .leading)
        HStack {
          ForEach(Array(axisYearLabels.enumerated()), id: \.offset) { index, label in
            if index > 0 { Spacer() }
            Text(label)
              .font(TrawlDesign.Typography.monoAxis)
              .foregroundStyle(TrawlDesign.Colors.mutedInk)
          }
        }
        Text("Total")
          .font(.system(size: 11, weight: .semibold))
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
          .frame(width: 72, alignment: .trailing)
      }
      .padding(.horizontal, 16)
      .frame(height: 26)
      .background(TrawlDesign.Colors.surface.opacity(0.75))

      Divider()

      // Fixed-height rows (34pt each — never resizes, never jumps!)
      VStack(spacing: 0) {
        ForEach(datedTrawlers) { trawler in
          OverviewWeeklyCurveRow(
            trawler: trawler,
            status: statusesByID[trawler.id],
            totalCount: operabilityByID[trawler.id]?.archivedItemCount ?? 0,
            accentColor: iconStore.laneAccentColor(for: trawler.id),
            windowStart: bounds.start,
            windowEnd: bounds.end,
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
            .font(.system(size: 11.5))
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
        Spacer()
        Button("Show all items →", action: onBrowseAllItems)
          .buttonStyle(.plain)
          .font(.system(size: 11.5, weight: .medium))
          .foregroundStyle(TrawlDesign.Colors.selectBlue)
      }
      .padding(.horizontal, 16)
      .padding(.vertical, 10)
      .background(TrawlDesign.Colors.surface.opacity(0.55))
    }
    .background(
      TrawlDesign.Colors.paper,
      in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface, style: .continuous)
        .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
    )
    .clipShape(RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface, style: .continuous))
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
  let windowStart: Date
  let windowEnd: Date
  @Binding var hoveredWeek: HoveredTimelineWeek?
  @Binding var dragRange: TimelineDragSelection?
  let onBrowseApp: () -> Void
  let onSelectDateRange: (Date, Date) -> Void

  private var visibleBuckets: [ArchiveWeeklyRecordCount] {
    guard let buckets = status?.archiveWeeklyHistory?.weeklyRecordCountsInChronologicalOrder else {
      return []
    }
    return buckets.filter { bucket in
      bucket.utcWeekStartTime >= windowStart && bucket.utcWeekStartTime <= windowEnd
    }
  }

  private var isHoveredRow: Bool {
    hoveredWeek?.trawlerID == trawler.id
  }

  var body: some View {
    HStack(spacing: 12) {
      Button(action: onBrowseApp) {
        HStack(spacing: 8) {
          TrawlerIconView(registeredTrawler: trawler.id, size: 18)
          Text(trawler.registeredTrawlerDisplayName)
            .font(.system(size: 12.5, weight: isHoveredRow ? .semibold : .medium))
            .foregroundStyle(TrawlDesign.Colors.ink)
            .lineLimit(1)
        }
        .frame(width: 124, alignment: .leading)
        .contentShape(Rectangle())
      }
      .buttonStyle(.plain)

      GeometryReader { proxy in
        let width = max(proxy.size.width, 1)
        let height = proxy.size.height

        ZStack(alignment: .leading) {
          Canvas { context, size in
            drawLinearCurve(in: &context, size: size)
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
                let d1 = date(forFraction: min(startFrac, endFrac))
                let d2 = date(forFraction: max(startFrac, endFrac))
                onSelectDateRange(d1, d2)
              } else {
                let clickedDate = date(forFraction: endFrac)
                let weekStart =
                  Calendar(identifier: .gregorian).dateInterval(of: .weekOfYear, for: clickedDate)?.start
                  ?? clickedDate
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
    .padding(.horizontal, 16)
    .frame(height: 34)
    .background(isHoveredRow ? TrawlDesign.Colors.matchRed.opacity(0.05) : Color.clear)
  }

  private func date(forFraction fraction: CGFloat) -> Date {
    let span = max(windowEnd.timeIntervalSince(windowStart), 1)
    return windowStart.addingTimeInterval(TimeInterval(fraction) * span)
  }

  private func updateHover(fraction: CGFloat) {
    let targetDate = date(forFraction: fraction)
    let nearest = visibleBuckets.min { lhs, rhs in
      abs(lhs.utcWeekStartTime.timeIntervalSince(targetDate))
        < abs(rhs.utcWeekStartTime.timeIntervalSince(targetDate))
    }
    let weekStart = nearest?.utcWeekStartTime ?? targetDate
    let weekCount =
      nearest.flatMap { bucket -> UInt64? in
        abs(bucket.utcWeekStartTime.timeIntervalSince(targetDate)) < 14 * 86_400
          ? bucket.recordCount : 0
      } ?? 0
    hoveredWeek = HoveredTimelineWeek(
      trawlerID: trawler.id,
      trawlerName: trawler.registeredTrawlerDisplayName,
      weekStart: weekStart,
      count: weekCount,
      normalizedX: fraction
    )
  }

  private func drawLinearCurve(in context: inout GraphicsContext, size: CGSize) {
    let baselineY = size.height - 2
    var baseline = Path()
    baseline.move(to: CGPoint(x: 0, y: baselineY))
    baseline.addLine(to: CGPoint(x: size.width, y: baselineY))
    context.stroke(baseline, with: .color(TrawlDesign.Colors.hairline.opacity(0.55)), lineWidth: 0.75)

    let buckets = visibleBuckets
    guard !buckets.isEmpty else { return }
    let maxCount = Double(buckets.map(\.recordCount).max() ?? 0)
    guard maxCount > 0 else { return }

    let span = max(windowEnd.timeIntervalSince(windowStart), 1)
    let usableHeight = max(size.height - 5, 4)

    var strokePath = Path()
    var fillPath = Path()
    var previousWeekStart: Date?
    var previousX: CGFloat = 0
    var hasStarted = false

    for bucket in buckets {
      let frac = min(max(bucket.utcWeekStartTime.timeIntervalSince(windowStart) / span, 0), 1)
      let x = CGFloat(frac) * size.width
      // Honest linear scale (minimum 1.5pt lift when > 0 so quiet weeks remain visible).
      let norm = Double(bucket.recordCount) / maxCount
      let y = bucket.recordCount > 0
        ? baselineY - max(1.5, CGFloat(norm) * usableHeight)
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
        // Drop back to zero across multi-week gaps so empty periods stay honest.
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
      context.fill(fillPath, with: .color(accentColor.opacity(0.16)))
      context.stroke(
        strokePath,
        with: .color(accentColor.opacity(0.92)),
        style: StrokeStyle(lineWidth: 1.4, lineJoin: .round)
      )
    }
  }
}

private struct ConstellationConnectingLines: View {
  let size: CGSize

  var body: some View {
    Canvas { context, size in
      let rightColumnWidth: CGFloat = 308
      let hubWidth: CGFloat = 170
      let spacing: CGFloat = 18
      let rightEdgeX = max(size.width - rightColumnWidth, size.width * 0.65)
      let hubRightX = rightEdgeX - spacing
      let hubLeftX = hubRightX - hubWidth
      let leftEdgeX = max(120, hubLeftX - spacing)
      let centerY = size.height * 0.50

      for idx in 0..<5 {
        let y = size.height * (0.14 + CGFloat(idx) * 0.18)
        var path = Path()
        path.move(to: CGPoint(x: leftEdgeX, y: y))
        path.addCurve(
          to: CGPoint(x: hubLeftX, y: centerY + (CGFloat(idx) - 2) * 5),
          control1: CGPoint(x: leftEdgeX + spacing * 0.6, y: y),
          control2: CGPoint(x: hubLeftX - spacing * 0.6, y: centerY)
        )
        context.stroke(path, with: .color(TrawlDesign.Colors.hairline.opacity(0.6)), lineWidth: 1.2)
      }

      for idx in 0..<3 {
        let y = size.height * (0.20 + CGFloat(idx) * 0.30)
        var path = Path()
        path.move(to: CGPoint(x: hubRightX, y: centerY + (CGFloat(idx) - 1) * 7))
        path.addCurve(
          to: CGPoint(x: rightEdgeX, y: y),
          control1: CGPoint(x: hubRightX + spacing * 0.6, y: centerY),
          control2: CGPoint(x: rightEdgeX - spacing * 0.6, y: y)
        )
        context.stroke(path, with: .color(TrawlDesign.Colors.hairline.opacity(0.7)), lineWidth: 1.3)
      }
    }
  }
}
