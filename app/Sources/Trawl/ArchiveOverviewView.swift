import SwiftUI
import TrawlClient
import TrawlCore

/// Overview (Home) view:
/// 1. Top: "Your archive" actionable constellation connecting apps -> archive host -> AI & Other Macs
///    using dedicated connector columns so bezier curves never cross or overlap any card.
/// 2. Bottom: "Items per week" fixed-height weekly curves using the sqrt-recency time axis
///    (`ArchiveTimelineTimeAxis`) and power-law vertical intensity (`ArchiveTimelineIntensityScale`).
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
      let isWideLayout = outerProxy.size.width >= 900
      ScrollView {
        VStack(alignment: .leading, spacing: TrawlDesign.Space.md) {
          constellationCard(isWideLayout: isWideLayout)
          weeklyLinesCard
        }
        .padding(TrawlDesign.Space.md)
        .frame(width: outerProxy.size.width, alignment: .topLeading)
      }
      .background(TrawlDesign.Colors.surface)
    }
  }

  // MARK: - Top: Your archive constellation

  private func constellationCard(isWideLayout: Bool) -> some View {
    VStack(alignment: .leading, spacing: TrawlDesign.Space.md) {
      Text("Your archive")
        .font(TrawlDesign.Typography.sectionHeader)
        .foregroundStyle(TrawlDesign.Colors.ink)

      if isWideLayout {
        wideConstellation
      } else {
        compactConstellation
      }
    }
    .padding(TrawlDesign.Space.md)
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
    let rowCount = max(1, (trawlers.count + 1) / 2)
    let rightCardCount = isRemoteArchive ? 1 : 3

    return HStack(alignment: .center, spacing: 0) {
      appColumnsGrid
        .frame(maxWidth: .infinity, alignment: .leading)

      ConstellationConnectorColumn(
        direction: .leftToHub,
        branchCount: rowCount
      )
      .frame(width: 32)
      .frame(maxHeight: .infinity)

      centerHostHub
        .frame(width: 164)

      ConstellationConnectorColumn(
        direction: .hubToRight,
        branchCount: rightCardCount
      )
      .frame(width: 32)
      .frame(maxHeight: .infinity)

      rightConnectionCards
        .frame(width: 300)
    }
    .fixedSize(horizontal: false, vertical: true)
  }

  private var compactConstellation: some View {
    VStack(alignment: .leading, spacing: TrawlDesign.Space.md) {
      HStack(alignment: .top, spacing: TrawlDesign.Space.md) {
        centerHostHub
          .frame(width: 164)
        rightConnectionCards
          .frame(maxWidth: .infinity)
      }
      appColumnsGrid
    }
  }

  private var appColumnsGrid: some View {
    let split = (trawlers.count + 1) / 2
    let leftColumn = Array(trawlers.prefix(split))
    let rightColumn = Array(trawlers.dropFirst(split))

    return HStack(alignment: .top, spacing: TrawlDesign.Space.sm) {
      VStack(spacing: TrawlDesign.Space.xs) {
        ForEach(leftColumn) { trawler in
          constellationAppNode(for: trawler)
        }
      }
      if !rightColumn.isEmpty {
        VStack(spacing: TrawlDesign.Space.xs) {
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

    return HStack(spacing: TrawlDesign.Space.xs) {
      Button {
        onBrowseApp(trawler.id)
      } label: {
        HStack(spacing: TrawlDesign.Space.sm) {
          TrawlerIconView(registeredTrawler: trawler.id, size: 16)
          Text(trawler.registeredTrawlerDisplayName)
            .font(TrawlDesign.Typography.laneLabel)
            .foregroundStyle(TrawlDesign.Colors.ink)
            .lineLimit(1)
            .layoutPriority(1)
          Spacer(minLength: TrawlDesign.Space.xs)
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
        .font(TrawlDesign.Typography.caption)
        .foregroundStyle(TrawlDesign.Colors.warningAmber)
        .padding(.horizontal, TrawlDesign.Space.sm)
        .padding(.vertical, 2)
        .background(
          TrawlDesign.Colors.warningAmberSurface,
          in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.control, style: .continuous)
        )
        .fixedSize()
      }
    }
    .padding(.horizontal, TrawlDesign.Space.sm)
    .frame(maxWidth: .infinity, minHeight: 30, maxHeight: 30, alignment: .leading)
    .background(
      needsAttention ? TrawlDesign.Colors.warningAmberSurface : TrawlDesign.Colors.paper,
      in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.control, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: TrawlDesign.Radius.control, style: .continuous)
        .stroke(
          needsAttention
            ? TrawlDesign.Colors.warningAmber.opacity(0.45)
            : TrawlDesign.Colors.hairline,
          lineWidth: 1
        )
    )
    .help(operability?.statusLine ?? trawler.registeredTrawlerDisplayName)
  }

  private var centerHostHub: some View {
    Button(action: onOpenHostSwitcher) {
      VStack(spacing: TrawlDesign.Space.xs) {
        OpenTrawlMarkIcon(size: 26)
        Text(hostDisplayName)
          .font(TrawlDesign.Typography.sectionHeader)
          .foregroundStyle(TrawlDesign.Colors.ink)
          .lineLimit(1)
        Text("\(totalItemCount.formatted()) items")
          .font(TrawlDesign.Typography.monoSmall.weight(.semibold))
          .monospacedDigit()
          .foregroundStyle(TrawlDesign.Colors.ink)
        Text(freshnessLine)
          .font(TrawlDesign.Typography.caption)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
          .lineLimit(1)
      }
      .padding(.vertical, TrawlDesign.Space.md)
      .padding(.horizontal, TrawlDesign.Space.md)
      .frame(maxWidth: .infinity)
      .background(
        TrawlDesign.Colors.paper,
        in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface, style: .continuous)
      )
      .overlay(
        RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface, style: .continuous)
          .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
      )
    }
    .buttonStyle(.plain)
    .help("Switch archive host")
  }

  private var rightConnectionCards: some View {
    VStack(spacing: TrawlDesign.Space.sm) {
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
    VStack(alignment: .leading, spacing: TrawlDesign.Space.xs) {
      HStack(alignment: .center, spacing: TrawlDesign.Space.sm) {
        Text(title)
          .font(TrawlDesign.Typography.laneLabel.weight(.semibold))
          .foregroundStyle(TrawlDesign.Colors.ink)
          .lineLimit(1)
        Spacer(minLength: TrawlDesign.Space.xs)
        Button(buttonTitle, action: action)
          .buttonStyle(.plain)
          .font(TrawlDesign.Typography.caption.weight(.semibold))
          .foregroundStyle(isProminent ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.ink)
          .padding(.horizontal, TrawlDesign.Space.sm)
          .padding(.vertical, TrawlDesign.Space.xs)
          .background(
            isProminent ? TrawlDesign.Colors.selectBlueSurface : TrawlDesign.Colors.surface,
            in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.control, style: .continuous)
          )
          .fixedSize()
      }
      Text(subtitle)
        .font(TrawlDesign.Typography.meta)
        .foregroundStyle(TrawlDesign.Colors.mutedInk)
        .fixedSize(horizontal: false, vertical: true)
    }
    .padding(TrawlDesign.Space.md)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(
      TrawlDesign.Colors.paper,
      in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface, style: .continuous)
        .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
    )
  }

  // MARK: - Bottom: Per-App Sqrt-Recency + Power-Law Weekly Curves

  private var weeklyLinesCard: some View {
    let axis = timeAxis

    return VStack(spacing: 0) {
      HStack(spacing: TrawlDesign.Space.sm) {
        HStack(alignment: .firstTextBaseline, spacing: TrawlDesign.Space.sm) {
          Text("Items per week")
            .font(TrawlDesign.Typography.sectionHeader)
            .foregroundStyle(TrawlDesign.Colors.ink)
          Text("Drag across a row to pick dates.")
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
        Spacer(minLength: TrawlDesign.Space.sm)
        if let hovered = hoveredWeek {
          Button {
            let weekEnd = hovered.weekStart.addingTimeInterval(7 * 86_400 - 1)
            onSelectDateRange(hovered.trawlerID, hovered.weekStart, weekEnd)
          } label: {
            HStack(spacing: TrawlDesign.Space.sm) {
              Text("\(hovered.trawlerName) · week of \(hovered.weekStart.formatted(.dateTime.day().month(.abbreviated).year()))")
                .font(TrawlDesign.Typography.caption.weight(.semibold))
              Text("\(hovered.count.formatted()) items")
                .font(TrawlDesign.Typography.monoSmall)
              Text("Show →")
                .font(TrawlDesign.Typography.caption.weight(.semibold))
            }
            .foregroundStyle(TrawlDesign.Colors.matchRed)
            .padding(.horizontal, TrawlDesign.Space.sm)
            .padding(.vertical, TrawlDesign.Space.xs)
            .background(
              TrawlDesign.Colors.matchRed.opacity(0.10),
              in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.control, style: .continuous)
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
          VStack(alignment: .leading, spacing: TrawlDesign.Space.md) {
            Text("Choose dates")
              .font(TrawlDesign.Typography.sectionHeader)
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
              .tint(TrawlDesign.Colors.selectBlue)
            }
          }
          .padding(TrawlDesign.Space.md)
          .frame(width: 280)
        }

        HStack(spacing: 2) {
          ForEach(OverviewChartSpan.allCases) { span in
            let selected = chartSpan == span
            Button(span.rawValue) {
              chartSpan = span
            }
            .buttonStyle(.plain)
            .font(TrawlDesign.Typography.caption.weight(selected ? .semibold : .medium))
            .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk)
            .padding(.horizontal, TrawlDesign.Space.sm)
            .padding(.vertical, TrawlDesign.Space.xs)
            .background(
              selected ? TrawlDesign.Colors.selectBlueSurface : Color.clear,
              in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.control, style: .continuous)
            )
          }
        }
        .padding(2)
        .background(
          TrawlDesign.Colors.surface,
          in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.control, style: .continuous)
        )
        .overlay(
          RoundedRectangle(cornerRadius: TrawlDesign.Radius.control, style: .continuous)
            .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
        )
      }
      .padding(.horizontal, TrawlDesign.Space.md)
      .padding(.vertical, TrawlDesign.Space.sm)

      Divider()

      // Sqrt-recency axis row matching ArchiveTimelineTimeAxis ticks
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
      .frame(height: 24)
      .background(TrawlDesign.Colors.surface)

      Divider()

      // Fixed-height rows (30pt each — never resizes, never jumps)
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
        Button("Show all items →", action: onBrowseAllItems)
          .buttonStyle(.plain)
          .font(TrawlDesign.Typography.meta.weight(.medium))
          .foregroundStyle(TrawlDesign.Colors.selectBlue)
      }
      .padding(.horizontal, TrawlDesign.Space.md)
      .padding(.vertical, TrawlDesign.Space.sm)
      .background(TrawlDesign.Colors.surface)
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
          TrawlerIconView(registeredTrawler: trawler.id, size: 17)
          Text(trawler.registeredTrawlerDisplayName)
            .font(isHoveredRow ? TrawlDesign.Typography.laneLabel.weight(.semibold) : TrawlDesign.Typography.laneLabel)
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
                let clickedDate = axis.date(forDatedNormalizedPosition: Double(endFrac))
                let weekStart =
                  Calendar(identifier: .gregorian).dateInterval(of: .weekOfYear, for: clickedDate)?.start
                  ?? clickedDate
                onSelectDateRange(weekStart, weekStart.addingTimeInterval(7 * 86_400 - 1))
              }
            }
        )
      }
      .frame(height: 22)

      Text(totalCount.formatted())
        .font(TrawlDesign.Typography.monoSmall)
        .monospacedDigit()
        .foregroundStyle(TrawlDesign.Colors.mutedInk)
        .frame(width: 72, alignment: .trailing)
    }
    .padding(.horizontal, TrawlDesign.Space.md)
    .frame(height: 30)
    .background(isHoveredRow ? TrawlDesign.Colors.matchRed.opacity(0.05) : Color.clear)
  }

  private func updateHover(fraction: CGFloat) {
    let targetDate = axis.date(forDatedNormalizedPosition: Double(fraction))
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
      // Power-law vertical intensity (pow(ratio, 0.65)) so bursty spikes stay prominent
      // while ordinary active weeks remain clearly legible.
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

/// Dedicated fixed-width column for Constellation bezier curves so connectors
/// start and end cleanly at card borders and never draw behind or over content.
private struct ConstellationConnectorColumn: View {
  enum Direction {
    case leftToHub
    case hubToRight
  }

  let direction: Direction
  let branchCount: Int

  var body: some View {
    Canvas { context, size in
      let count = max(1, branchCount)
      let centerY = size.height / 2
      let w = size.width

      for idx in 0..<count {
        let fraction = (CGFloat(idx) + 0.5) / CGFloat(count)
        let outerY = size.height * fraction
        let hubOffset = (fraction - 0.5) * min(size.height * 0.28, 44)
        let hubY = centerY + hubOffset

        var path = Path()
        switch direction {
        case .leftToHub:
          path.move(to: CGPoint(x: 0, y: outerY))
          path.addCurve(
            to: CGPoint(x: w, y: hubY),
            control1: CGPoint(x: w * 0.55, y: outerY),
            control2: CGPoint(x: w * 0.45, y: hubY)
          )
        case .hubToRight:
          path.move(to: CGPoint(x: 0, y: hubY))
          path.addCurve(
            to: CGPoint(x: w, y: outerY),
            control1: CGPoint(x: w * 0.55, y: hubY),
            control2: CGPoint(x: w * 0.45, y: outerY)
          )
        }
        context.stroke(
          path,
          with: .color(TrawlDesign.Colors.hairline),
          style: StrokeStyle(lineWidth: 1.2, lineCap: .round)
        )
      }
    }
  }
}
