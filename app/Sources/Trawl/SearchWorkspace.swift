import SwiftUI
import TrawlClient
import TrawlCore

enum SearchFocus: Hashable {
  case field
  case results
  case record
}

enum SearchEscapeAction: Equatable {
  case closeRecord
  case focusField
  case dismiss

  static func resolve(showsRecord: Bool, focus: SearchFocus?) -> Self {
    if showsRecord { return .closeRecord }
    return focus == .results ? .focusField : .dismiss
  }
}

enum SearchWorkspacePaneVisibility {
  static func showsRecord(for phase: SearchOpenPhase) -> Bool { phase != .idle }
}

enum SearchWorkspaceLayout: Equatable {
  case results
  case compactRecord
  case split

  static func resolve(
    isCompact: Bool,
    showsCompactRecord: Bool,
    openPhase: SearchOpenPhase
  ) -> Self {
    if isCompact {
      return showsCompactRecord ? .compactRecord : .results
    }
    return SearchWorkspacePaneVisibility.showsRecord(for: openPhase) ? .split : .results
  }
}

enum SearchWorkspaceGeometry {
  static let wideResultsWidth = TrawlDesign.searchResultsMaximumWidth

  struct WideLayout: Equatable {
    let resultsOriginX: CGFloat
    let reservesRecordRegion: Bool
  }

  static func layout(for workspace: SearchWorkspaceLayout) -> WideLayout {
    switch workspace {
    case .results, .split:
      WideLayout(resultsOriginX: 0, reservesRecordRegion: true)
    case .compactRecord:
      WideLayout(resultsOriginX: 0, reservesRecordRegion: false)
    }
  }
}

enum SearchWorkspaceFieldContent: Equatable {
  case none
  case scopedPrompt

  static func resolve(isScoped: Bool) -> Self {
    isScoped ? .scopedPrompt : .none
  }
}

/// Main archive workspace hosting the fixed-order left app rail alongside the
/// explicit `Overview` | `Search` destination.
struct ArchiveWorkspaceView: View {
  let client: any TrawlClient
  let trawlers: [RestingTrawler]
  let searchableTrawlers: [RestingTrawler]
  let trawlerStatuses: [TrawlerStatus]
  let operabilityByID: [RegisteredTrawlerIdentity: AppOperabilityState]
  let hostDisplayName: String
  let freshnessLine: String
  let isRemoteArchive: Bool
  let hasCopiedAIPrompt: Bool
  let focusRequestToken: Int
  @Binding var destination: WorkspaceDestination

  let onCopyAIPrompt: () -> Void
  let onOpenAIAppsSettings: () -> Void
  let onOpenOtherMacsSettings: () -> Void
  let onOpenPluginsSettings: () -> Void
  let onOpenHostSwitcher: () -> Void
  let onRunAttentionAction: (RegisteredTrawlerIdentity, AppAttentionAction) -> Void

  @State private var model: SearchModel
  @State private var interaction: SearchInteraction
  @State private var trawlerResolver: SearchTrawlerResolver
  @State private var fieldState = SearchFieldState()
  @FocusState private var focus: SearchFocus?

  init(
    client: any TrawlClient,
    trawlers: [RestingTrawler],
    searchableTrawlers: [RestingTrawler],
    trawlerStatuses: [TrawlerStatus],
    operabilityByID: [RegisteredTrawlerIdentity: AppOperabilityState],
    hostDisplayName: String,
    freshnessLine: String,
    isRemoteArchive: Bool,
    hasCopiedAIPrompt: Bool,
    focusRequestToken: Int = 0,
    destination: Binding<WorkspaceDestination>,
    onCopyAIPrompt: @escaping () -> Void,
    onOpenAIAppsSettings: @escaping () -> Void,
    onOpenOtherMacsSettings: @escaping () -> Void,
    onOpenPluginsSettings: @escaping () -> Void,
    onOpenHostSwitcher: @escaping () -> Void,
    onRunAttentionAction: @escaping (RegisteredTrawlerIdentity, AppAttentionAction) -> Void
  ) {
    self.client = client
    self.trawlers = trawlers
    self.searchableTrawlers = searchableTrawlers
    self.trawlerStatuses = trawlerStatuses
    self.operabilityByID = operabilityByID
    self.hostDisplayName = hostDisplayName
    self.freshnessLine = freshnessLine
    self.isRemoteArchive = isRemoteArchive
    self.hasCopiedAIPrompt = hasCopiedAIPrompt
    self.focusRequestToken = focusRequestToken
    self._destination = destination
    self.onCopyAIPrompt = onCopyAIPrompt
    self.onOpenAIAppsSettings = onOpenAIAppsSettings
    self.onOpenOtherMacsSettings = onOpenOtherMacsSettings
    self.onOpenPluginsSettings = onOpenPluginsSettings
    self.onOpenHostSwitcher = onOpenHostSwitcher
    self.onRunAttentionAction = onRunAttentionAction

    let searchModel = SearchModel(client: client)
    _model = State(initialValue: searchModel)
    _interaction = State(
      initialValue: SearchInteraction(model: searchModel, registeredTrawler: nil)
    )
    _trawlerResolver = State(
      initialValue: SearchTrawlerResolver(statuses: trawlerStatuses)
    )
  }

  private var statusesByID: [RegisteredTrawlerIdentity: TrawlerStatus] {
    Dictionary(uniqueKeysWithValues: trawlerStatuses.map { ($0.id, $0) })
  }

  private var totalArchivedItemCount: UInt64 {
    operabilityByID.values.reduce(UInt64(0)) { $0 + $1.archivedItemCount }
  }

  private var searchMatchCountByID: [RegisteredTrawlerIdentity: UInt64] {
    Dictionary(
      uniqueKeysWithValues: model.trawlerSearchResults.map {
        ($0.registeredTrawler, $0.totalSearchMatches)
      }
    )
  }

  private var selectedScopeTrawler: RestingTrawler? {
    guard let id = interaction.registeredTrawler else { return nil }
    return trawlers.first(where: { $0.id == id })
  }

  var body: some View {
    GeometryReader { proxy in
      let isCompactRail = proxy.size.width < TrawlDesign.compactRailBreakpoint
      let contentWidth = max(proxy.size.width - (isCompactRail ? TrawlDesign.compactSidebarWidth : TrawlDesign.wideSidebarWidth), 320)

      HStack(spacing: 0) {
        AppSidebarRailView(
          mode: destination,
          isCompact: isCompactRail,
          trawlers: trawlers,
          operabilityByID: operabilityByID,
          searchMatchCountByID: searchMatchCountByID,
          totalAllAppsCount: totalArchivedItemCount,
          totalSearchResultsCount: model.totalMatchedRecordCount,
          totalSearchCountIsLowerBound: model.totalMatchedCountIsLowerBound,
          hasActiveDateRange: interaction.hasActiveDateRange,
          selectedTrawlerID: destination == .overview ? nil : interaction.registeredTrawler,
          hostDisplayName: hostDisplayName,
          hostSubtitle: freshnessLine,
          isRemoteArchive: isRemoteArchive,
          onSelectTrawler: { trawlerID in
            interaction.changeScope(to: trawlerID)
            destination = .search
          },
          onOpenHostSwitcher: onOpenHostSwitcher,
          onOpenPluginsSettings: onOpenPluginsSettings
        )

        Divider()

        VStack(spacing: 0) {
          SearchField(
            query: $interaction.query,
            scope: selectedScopeTrawler,
            availableTrawlers: searchableTrawlers,
            earliestDateInclusive: interaction.earliestMatchingArchiveRecordTimeInclusive,
            latestDateInclusive: interaction.latestMatchingArchiveRecordTimeInclusive,
            focus: $focus,
            onSelectScope: { trawler in
              interaction.changeScope(to: trawler?.id)
              destination = .search
            },
            onClearScope: {
              interaction.changeScope(to: nil)
            },
            onSetDateRange: { earliest, latest in
              interaction.changeDateRange(earliestInclusive: earliest, latestInclusive: latest)
              destination = .search
            },
            onClearDateRange: {
              interaction.clearDateRange()
            },
            onSubmit: {
              destination = .search
              openSelectedResult()
            },
            onMoveToResults: {
              destination = .search
              focusResults()
            }
          )
          .id(fieldState.identity)
          .padding(.horizontal, 14)
          .padding(.vertical, 10)
          .background(TrawlDesign.Colors.paper)

          Divider()

          switch destination {
          case .overview:
            ArchiveOverviewView(
              trawlers: trawlers,
              statusesByID: statusesByID,
              operabilityByID: operabilityByID,
              hostDisplayName: hostDisplayName,
              freshnessLine: freshnessLine,
              totalItemCount: totalArchivedItemCount,
              isRemoteArchive: isRemoteArchive,
              hasCopiedAIPrompt: hasCopiedAIPrompt,
              onCopyAIPrompt: onCopyAIPrompt,
              onOpenAIAppsSettings: onOpenAIAppsSettings,
              onOpenOtherMacsSettings: onOpenOtherMacsSettings,
              onOpenHostSwitcher: onOpenHostSwitcher,
              onRunAttentionAction: onRunAttentionAction,
              onBrowseApp: { trawlerID in
                interaction.applyScopeAndDateRange(
                  registeredTrawler: trawlerID,
                  earliestInclusive: nil,
                  latestInclusive: nil
                )
                destination = .search
              },
              onBrowseAllItems: {
                interaction.applyScopeAndDateRange(
                  registeredTrawler: nil,
                  earliestInclusive: nil,
                  latestInclusive: nil
                )
                destination = .search
              },
              onSelectDateRange: { trawlerID, start, end in
                interaction.applyScopeAndDateRange(
                  registeredTrawler: trawlerID,
                  earliestInclusive: start,
                  latestInclusive: end
                )
                destination = .search
              }
            )
          case .search:
            searchBody(availableWidth: contentWidth)
          }
        }
        .frame(width: contentWidth, height: proxy.size.height)
        .clipped()
      }
      .background(TrawlDesign.Colors.paper)
    }
    .onChange(of: interaction.query) { _, newValue in
      if !newValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && destination != .search {
        destination = .search
      }
    }
    .onChange(of: model.phase) { _, newPhase in
      if newPhase != .loading {
        interaction.reconcileCommittedResults()
        if interaction.selectedSearchMatchIdentifier == nil, let first = model.searchMatches.first {
          interaction.selectedSearchMatchIdentifier = first.id
          Task { await model.open(first) }
        }
      }
    }
    .onChange(of: fieldState.focusRequest) { _, _ in
      Task { @MainActor in
        focus = .field
      }
    }
    .onChange(of: focusRequestToken) { _, _ in
      fieldState.requestFocus()
    }
    .onChange(of: trawlerStatuses) { _, statuses in
      trawlerResolver.replace(with: statuses)
    }
    .onKeyPress(.escape) {
      handleEscape()
      return .handled
    }
    .onExitCommand(perform: handleEscape)
    .task(
      id: SearchTriggerKey(
        destination: destination,
        query: interaction.query,
        registeredTrawler: interaction.registeredTrawler,
        earliestDateInclusive: interaction.earliestMatchingArchiveRecordTimeInclusive,
        latestDateInclusive: interaction.latestMatchingArchiveRecordTimeInclusive
      )
    ) {
      guard destination == .search else { return }
      await model.search(
        interaction.query,
        registeredTrawler: interaction.registeredTrawler,
        earliestMatchingArchiveRecordTimeInclusive:
          interaction.earliestMatchingArchiveRecordTimeInclusive,
        latestMatchingArchiveRecordTimeInclusive:
          interaction.latestMatchingArchiveRecordTimeInclusive,
        allowEmptyBrowse: true
      )
    }
  }

  private func searchBody(availableWidth: CGFloat) -> some View {
    let resultsColumnWidth = min(
      max(264, availableWidth * 0.40),
      TrawlDesign.searchResultsMaximumWidth
    )

    return VStack(spacing: 0) {
      Group {
        switch SearchWorkspaceMode.resolve(
          phase: model.phase,
          resultCount: model.searchMatches.count
        ) {
        case .field:
          if selectedScopeTrawler?.id.registeredTrawlerIdentity == "contacts" {
            ContentUnavailableView(
              "Search Contacts",
              systemImage: "person.crop.circle",
              description: Text("Type a name, email, or phone number to search Contacts.")
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
          } else {
            SearchOutcome(
              phase: model.phase,
              isScoped: selectedScopeTrawler != nil,
              timedOutLocally: model.timedOutLocally
            )
          }
        case .outcome:
          SearchOutcome(
            phase: model.phase,
            isScoped: selectedScopeTrawler != nil,
            timedOutLocally: model.timedOutLocally
          )
        case .results:
          HStack(spacing: 0) {
            SearchResultsList(
              phase: model.phase,
              searchMatches: model.searchMatches,
              totalMatchedRecordCount: model.totalMatchedRecordCount,
              totalMatchedCountIsLowerBound: model.totalMatchedCountIsLowerBound,
              isTruncated: model.isTruncated,
              isLoadingMore: model.isLoadingMore,
              canLoadMore: model.canLoadMore,
              loadMoreFailureMessage: model.loadMoreFailureMessage,
              trawlerDisplayName: {
                $0.map(trawlerDisplayName(for:))
                  ?? SearchTrawlerResolver.unavailableDisplayName
              },
              showsTrawlerDisplayName: interaction.registeredTrawler == nil,
              committedQuery: model.committedInput?.query,
              resultLimit: model.resultLimit,
              title: model.displayTitle(for:),
              selectedSearchMatchIdentifier: $interaction.selectedSearchMatchIdentifier,
              focus: $focus,
              onReturn: openSelectedResult,
              onEscape: handleEscape,
              onOpen: open,
              onSelectionChanged: open,
              onLoadMore: {
                Task { await model.loadMore() }
              }
            )
            .frame(width: resultsColumnWidth)

            Divider()

            if model.openPhase == .idle {
              VStack(spacing: 6) {
                Text("Select a result to open it")
                  .font(TrawlDesign.Typography.body)
                  .foregroundStyle(TrawlDesign.Colors.mutedInk)
              }
              .frame(maxWidth: .infinity, maxHeight: .infinity)
              .background(TrawlDesign.Colors.surface.opacity(0.35))
            } else {
              ResultPreview(phase: model.openPhase, onOpenLink: openLinkedRecord)
            }
          }
        }
      }
      .frame(maxWidth: .infinity, maxHeight: .infinity)

      SearchDateLineScrubberView(
        trawlerStatuses: trawlerStatuses,
        scopedTrawlerID: interaction.registeredTrawler,
        queryText: interaction.query.trimmingCharacters(in: .whitespacesAndNewlines),
        resultSummaryText: scrubberResultSummary,
        earliestDateInclusive: interaction.earliestMatchingArchiveRecordTimeInclusive,
        latestDateInclusive: interaction.latestMatchingArchiveRecordTimeInclusive,
        onCommitDateRange: { start, end in
          let calendar = Calendar.current
          let rangeStart = calendar.startOfDay(for: min(start, end))
          let rangeEnd = ArchiveTimelineTimeAxis.inclusiveEndOfDay(
            for: max(start, end),
            calendar: calendar
          )
          interaction.changeDateRange(earliestInclusive: rangeStart, latestInclusive: rangeEnd)
        },
        onClearDateRange: {
          interaction.clearDateRange()
        }
      )
    }
  }

  private var scrubberResultSummary: String {
    guard !model.searchMatches.isEmpty else { return "" }
    let total = max(UInt64(model.searchMatches.count), model.totalMatchedRecordCount)
    if model.totalMatchedCountIsLowerBound {
      return "at least \(total.formatted()) results"
    }
    return total == 1 ? "1 result" : "\(total.formatted()) results"
  }

  private func trawlerDisplayName(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> String {
    if registeredTrawler == selectedScopeTrawler?.id {
      return selectedScopeTrawler?.registeredTrawlerDisplayName
        ?? SearchTrawlerResolver.unavailableDisplayName
    }
    return model.trawlerDisplayName(
      for: registeredTrawler,
      resolvedName: trawlerResolver.displayName(for: registeredTrawler)
    )
  }

  private func openLinkedRecord(
    _ link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier
  ) {
    Task {
      await model.open(link: link, anchor: anchor)
    }
  }

  private func focusResults() {
    guard let first = model.searchMatches.first else { return }
    if interaction.selectedSearchMatchIdentifier == nil {
      interaction.selectedSearchMatchIdentifier = first.id
    }
    focus = .results
  }

  private func openSelectedResult() {
    guard interaction.resultForReturn() != nil else { return }
    Task { await interaction.handleReturn() }
  }

  private func open(_ searchMatch: SearchMatch) {
    interaction.selectedSearchMatchIdentifier = searchMatch.id
    Task { await model.open(searchMatch) }
  }

  private func handleEscape() {
    if focus == .results {
      focus = .field
    } else if !interaction.query.isEmpty || interaction.hasActiveDateRange || interaction.registeredTrawler != nil {
      interaction.query = ""
      interaction.applyScopeAndDateRange(
        registeredTrawler: nil,
        earliestInclusive: nil,
        latestInclusive: nil
      )
      focus = .field
    }
  }
}

private struct SearchField: View {
  @Environment(\.locale) private var locale

  @Binding var query: String
  let scope: RestingTrawler?
  let availableTrawlers: [RestingTrawler]
  let earliestDateInclusive: Date?
  let latestDateInclusive: Date?
  @FocusState.Binding var focus: SearchFocus?
  let onSelectScope: (RestingTrawler?) -> Void
  let onClearScope: () -> Void
  let onSetDateRange: (Date, Date) -> Void
  let onClearDateRange: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void

  @State private var isShowingDatePicker = false
  @State private var draftStartDate = Calendar.current.date(byAdding: .day, value: -30, to: Date()) ?? Date()
  @State private var draftEndDate = Date()

  private var dateRangeLabel: String? {
    ArchiveTimelineDateRangeFormatting.label(
      earliestInclusive: earliestDateInclusive,
      latestInclusive: latestDateInclusive,
      locale: locale
    )
  }

  var body: some View {
    HStack(spacing: 8) {
      Image(systemName: "magnifyingglass")
        .font(.body.weight(.medium))
        .foregroundStyle(TrawlDesign.Colors.mutedInk)

      TextField(
        scope.map { "Search \($0.registeredTrawlerDisplayName)" }
          ?? "Search your apps",
        text: $query
      )
      .font(TrawlDesign.Typography.body)
      .textFieldStyle(.plain)
      .focused($focus, equals: .field)
      .layoutPriority(1)
      .onSubmit(onSubmit)
      .onKeyPress(.downArrow) {
        onMoveToResults()
        return .handled
      }

      if let dateRangeLabel {
        activeFilterChip(
          title: dateRangeLabel,
          systemImage: "calendar",
          accessibilityClearLabel: "Clear date filter",
          onClear: onClearDateRange
        )
      }

      if let scope {
        activeAppChip(scope: scope, onClear: onClearScope)
      }

      dateFilterButton

      if !query.isEmpty {
        Button(action: clearQuery) {
          Image(systemName: "xmark.circle.fill")
            .font(.body)
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
            .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .help("Clear search")
        .accessibilityLabel("Clear search")
      }
    }
    .padding(.horizontal, 12)
    .frame(height: 38)
    .background(
      TrawlDesign.Colors.surface,
      in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface)
    )
    .overlay {
      RoundedRectangle(cornerRadius: TrawlDesign.Radius.surface)
        .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
    }
  }

  private var dateFilterButton: some View {
    Button {
      draftStartDate =
        earliestDateInclusive
        ?? Calendar.current.date(byAdding: .day, value: -30, to: Date())
        ?? Date()
      draftEndDate = latestDateInclusive ?? Date()
      isShowingDatePicker.toggle()
    } label: {
      HStack(spacing: 5) {
        Image(systemName: "calendar")
          .font(.caption.weight(.semibold))
        Text("Dates")
          .font(.callout.weight(.medium))
          .lineLimit(1)
      }
      .foregroundStyle(TrawlDesign.Colors.mutedInk)
      .padding(.horizontal, 9)
      .padding(.vertical, 4)
      .background(TrawlDesign.Colors.hairline.opacity(0.45), in: Capsule())
      .fixedSize()
    }
    .buttonStyle(.plain)
    .fixedSize(horizontal: true, vertical: false)
    .help("Choose dates")
    .accessibilityLabel("Filter by dates")
    .popover(isPresented: $isShowingDatePicker, arrowEdge: .bottom) {
      VStack(alignment: .leading, spacing: 12) {
        Text("Choose dates")
          .font(TrawlDesign.Typography.sectionHeader)
        HStack(spacing: 6) {
          quickDateRangeButton("7 days", inclusiveDays: 7)
          quickDateRangeButton("30 days", inclusiveDays: 30)
          quickDateRangeButton("1 year", inclusiveDays: 365)
        }
        Divider()
        DatePicker("From", selection: $draftStartDate, displayedComponents: .date)
        DatePicker("To", selection: $draftEndDate, displayedComponents: .date)
        HStack {
          if earliestDateInclusive != nil || latestDateInclusive != nil {
            Button("Clear") {
              onClearDateRange()
              isShowingDatePicker = false
            }
          }
          Spacer()
          Button("Apply") {
            let low = min(draftStartDate, draftEndDate)
            let high = max(draftStartDate, draftEndDate)
            let calendar = Calendar.current
            let start = calendar.startOfDay(for: low)
            let end = ArchiveTimelineTimeAxis.inclusiveEndOfDay(for: high, calendar: calendar)
            onSetDateRange(start, max(start, end))
            isShowingDatePicker = false
          }
          .buttonStyle(.borderedProminent)
          .tint(TrawlDesign.Colors.selectBlue)
        }
      }
      .padding(16)
      .frame(width: 280)
    }
  }

  private func quickDateRangeButton(_ title: String, inclusiveDays: Int) -> some View {
    Button(title) {
      let calendar = Calendar.current
      let now = Date()
      let precedingDays = max(0, inclusiveDays - 1)
      let rawStart = calendar.date(byAdding: .day, value: -precedingDays, to: now) ?? now
      let start = calendar.startOfDay(for: rawStart)
      let end = ArchiveTimelineTimeAxis.inclusiveEndOfDay(for: now, calendar: calendar)
      onSetDateRange(start, max(start, end))
      isShowingDatePicker = false
    }
    .buttonStyle(.bordered)
    .controlSize(.small)
  }

  private func activeFilterChip(
    title: String,
    systemImage: String,
    accessibilityClearLabel: String,
    onClear: @escaping () -> Void
  ) -> some View {
    HStack(spacing: 5) {
      Image(systemName: systemImage)
        .font(.caption.weight(.semibold))
      Text(title)
        .font(.callout.weight(.medium))
        .lineLimit(1)
      Button(action: onClear) {
        Image(systemName: "xmark")
          .font(.caption2.weight(.bold))
          .padding(3)
          .contentShape(.circle)
      }
      .buttonStyle(.plain)
      .accessibilityLabel(accessibilityClearLabel)
    }
    .foregroundStyle(TrawlDesign.Colors.selectBlue)
    .padding(.leading, 9)
    .padding(.trailing, 6)
    .padding(.vertical, 3)
    .background(TrawlDesign.Colors.selectBlueSurface, in: Capsule())
    .overlay {
      Capsule()
        .stroke(TrawlDesign.Colors.selectBlue.opacity(0.32), lineWidth: 1)
    }
    .fixedSize()
  }

  private func activeAppChip(
    scope: RestingTrawler,
    onClear: @escaping () -> Void
  ) -> some View {
    HStack(spacing: 5) {
      TrawlerIconView(registeredTrawler: scope.id, size: 15)
      Text(scope.registeredTrawlerDisplayName)
        .font(.callout.weight(.semibold))
        .lineLimit(1)
      Button(action: onClear) {
        Image(systemName: "xmark")
          .font(.caption2.weight(.bold))
          .padding(3)
          .contentShape(.circle)
      }
      .buttonStyle(.plain)
      .accessibilityLabel("Clear app filter")
    }
    .foregroundStyle(TrawlDesign.Colors.selectBlue)
    .padding(.leading, 7)
    .padding(.trailing, 6)
    .padding(.vertical, 3)
    .background(TrawlDesign.Colors.selectBlueSurface, in: Capsule())
    .overlay {
      Capsule()
        .stroke(TrawlDesign.Colors.selectBlue.opacity(0.32), lineWidth: 1)
    }
    .fixedSize()
  }

  private func clearQuery() {
    query = ""
    Task { @MainActor in
      focus = .field
    }
  }
}

private struct SearchOutcome: View {
  let phase: SearchPhase
  let isScoped: Bool
  let timedOutLocally: Bool

  var body: some View {
    Group {
      switch phase {
      case .loading:
        VStack(spacing: 9) {
          ProgressView()
            .controlSize(.small)
          Text("Searching. Stops after \(SearchModel.defaultWaitSeconds) seconds.")
        }
        .font(.callout)
        .foregroundStyle(.secondary)
      default:
        ContentUnavailableView(
          OperationalCopy.Search.outcomeTitle(for: phase),
          systemImage: SearchWorkspaceCopy.outcomeSymbol(for: phase),
          description: Text(detail)
        )
      }
    }
    .multilineTextAlignment(.center)
    .frame(maxWidth: .infinity, maxHeight: .infinity)
    .padding()
  }

  private var detail: String {
    OperationalCopy.Search.outcomeDetail(
      for: phase,
      isScoped: isScoped,
      timedOutLocally: timedOutLocally,
      timeoutSeconds: SearchModel.defaultWaitSeconds
    )
  }
}

struct SearchKey: Hashable {
  let query: String
  let registeredTrawler: RegisteredTrawlerIdentity?
  let earliestDateInclusive: Date?
  let latestDateInclusive: Date?
}

private struct SearchTriggerKey: Hashable {
  let destination: WorkspaceDestination
  let query: String
  let registeredTrawler: RegisteredTrawlerIdentity?
  let earliestDateInclusive: Date?
  let latestDateInclusive: Date?
}
