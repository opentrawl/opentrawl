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
    return openPhase != .idle ? .split : .results
  }
}

enum SearchWorkspaceFieldContent: Equatable {
  case none
  case scopedPrompt

  static func resolve(isScoped: Bool) -> Self {
    isScoped ? .scopedPrompt : .none
  }
}

/// Main archive workspace built on native macOS `NavigationSplitView`,
/// `.listStyle(.sidebar)`, and toolbar `.searchable`.
struct ArchiveWorkspaceView<DetailBanners: View>: View {
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
  /// Shown above the content column. A banner placed above the whole split view
  /// pushes the sidebar and the content under the toolbar.
  let detailBanners: DetailBanners

  @State private var model: SearchModel
  @State private var interaction: SearchInteraction
  @State private var trawlerResolver: SearchTrawlerResolver
  @State private var sidebarSelection: ArchiveSidebarSelection? = .overview
  @State private var columnVisibility: NavigationSplitViewVisibility = .all
  @State private var isSearchPresented = false
  @State private var isShowingDatePicker = false
  @State private var draftStartDate = Calendar.current.date(byAdding: .day, value: -30, to: Date()) ?? Date()
  @State private var draftEndDate = Date()
  @State private var lastDetailWidth: CGFloat = 960
  @State private var searchRetryToken = 0
  @State private var searchMatchCountsFromLastSearchOfAllApps: SearchMatchCountsForAllApps?
  @State private var emptyBrowseCountByID: [RegisteredTrawlerIdentity: UInt64] = [:]
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
    onRunAttentionAction: @escaping (RegisteredTrawlerIdentity, AppAttentionAction) -> Void,
    @ViewBuilder detailBanners: () -> DetailBanners
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
    self.detailBanners = detailBanners()

    let searchModel = SearchModel(client: client)
    _model = State(initialValue: searchModel)
    _interaction = State(initialValue: SearchInteraction(model: searchModel, registeredTrawler: nil))
    _trawlerResolver = State(initialValue: SearchTrawlerResolver(statuses: trawlerStatuses))
    _sidebarSelection = State(
      initialValue: destination.wrappedValue == .search ? .allItems : .overview
    )
  }

  private var statusesByID: [RegisteredTrawlerIdentity: TrawlerStatus] {
    Dictionary(trawlerStatuses.map { ($0.id, $0) }, uniquingKeysWith: { _, latest in latest })
  }

  private var selectedScopeTrawler: RestingTrawler? {
    guard let registeredTrawler = interaction.registeredTrawler else { return nil }
    return trawlers.first(where: { $0.id == registeredTrawler })
  }

  /// A search of one app returns counts for that app only. The other apps keep
  /// the counts from the last search of all apps for the same words and dates,
  /// so choosing an app never blanks the counts beside it.
  private var searchMatchCountByID: [RegisteredTrawlerIdentity: AppSearchMatchCount] {
    var counts = searchMatchCountsFromLastSearchOfAllAppsForCurrentQueryAndDates?
      .searchMatchCountByRegisteredTrawler ?? [:]
    for trawlerSearchResult in model.trawlerSearchResults {
      counts[trawlerSearchResult.registeredTrawler] = AppSearchMatchCount(
        searchMatchCount: trawlerSearchResult.totalSearchMatches,
        isLowerBound: trawlerSearchResult.totalSearchMatchesIsLowerBound)
    }
    return counts
  }

  private var searchMatchCountsFromLastSearchOfAllAppsForCurrentQueryAndDates:
    SearchMatchCountsForAllApps?
  {
    guard interaction.registeredTrawler != nil,
      let counts = searchMatchCountsFromLastSearchOfAllApps,
      counts.query == interaction.query.trimmingCharacters(in: .whitespacesAndNewlines),
      counts.earliestDateInclusive == interaction.earliestMatchingArchiveRecordTimeInclusive,
      counts.latestDateInclusive == interaction.latestMatchingArchiveRecordTimeInclusive
    else {
      return nil
    }
    return counts
  }

  private var allAppsSearchMatchCountText: String? {
    if interaction.registeredTrawler == nil {
      guard model.totalMatchedRecordCount > 0 else { return nil }
      return AppSearchMatchCount(
        searchMatchCount: model.totalMatchedRecordCount,
        isLowerBound: model.totalMatchedCountIsLowerBound
      ).text
    }
    return searchMatchCountsFromLastSearchOfAllAppsForCurrentQueryAndDates?
      .totalSearchMatchCount.text
  }

  private var effectiveOperabilityByID: [RegisteredTrawlerIdentity: AppOperabilityState] {
    guard !emptyBrowseCountByID.isEmpty else { return operabilityByID }
    var merged = operabilityByID
    for (trawlerID, liveCount) in emptyBrowseCountByID {
      guard let existing = merged[trawlerID] else { continue }
      merged[trawlerID] = AppOperabilityState(
        archivedItemCount: liveCount,
        statusLine: existing.statusLine,
        needsAttention: existing.needsAttention,
        actionTitle: existing.actionTitle,
        action: existing.action
      )
    }
    return merged
  }

  private var allAppsChipCountText: String? {
    if isEmptyBrowse {
      let total = trawlers.reduce(UInt64(0)) { partial, trawler in
        guard trawler.id.registeredTrawlerIdentity != "contacts" else { return partial }
        return partial + (effectiveOperabilityByID[trawler.id]?.archivedItemCount ?? 0)
      }
      return total > 0 ? total.formatted() : allAppsSearchMatchCountText
    }
    return allAppsSearchMatchCountText
  }

  private var searchAppFilterChips: [SearchAppFilterChip] {
    if isEmptyBrowse {
      return trawlers.compactMap { trawler in
        guard trawler.id.registeredTrawlerIdentity != "contacts" else { return nil }
        let count = effectiveOperabilityByID[trawler.id]?.archivedItemCount ?? 0
        guard count > 0 else { return nil }
        return SearchAppFilterChip(
          registeredTrawler: trawler.id,
          registeredTrawlerDisplayName: trawler.registeredTrawlerDisplayName,
          searchMatchCountText: count.formatted())
      }
    }
    let counts = searchMatchCountByID
    return trawlers.compactMap { trawler in
      guard let count = counts[trawler.id], count.searchMatchCount > 0 else { return nil }
      return SearchAppFilterChip(
        registeredTrawler: trawler.id,
        registeredTrawlerDisplayName: trawler.registeredTrawlerDisplayName,
        searchMatchCountText: count.text)
    }
  }

  private var hasActiveDateRange: Bool {
    interaction.earliestMatchingArchiveRecordTimeInclusive != nil
      || interaction.latestMatchingArchiveRecordTimeInclusive != nil
  }

  private var isEmptyBrowse: Bool {
    destination == .search
      && interaction.query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
      && !hasActiveDateRange
  }

  private var activeDateRangeLabel: String? {
    ArchiveTimelineDateRangeFormatting.label(
      earliestInclusive: interaction.earliestMatchingArchiveRecordTimeInclusive,
      latestInclusive: interaction.latestMatchingArchiveRecordTimeInclusive
    )
  }

  private var navigationTitleText: String {
    switch destination {
    case .overview:
      return "Overview"
    case .search:
      if let scope = selectedScopeTrawler {
        return scope.registeredTrawlerDisplayName
      }
      return "All items"
    }
  }

  var body: some View {
    NavigationSplitView(columnVisibility: $columnVisibility) {
      AppSidebarRailView(
        selection: $sidebarSelection,
        isSearching: destination == .search,
        trawlers: trawlers,
        operabilityByID: effectiveOperabilityByID,
        searchMatchCountByID: searchMatchCountByID,
        allAppsSearchMatchCountText: allAppsSearchMatchCountText,
        hasActiveDateRange: hasActiveDateRange,
        isEmptyBrowse: isEmptyBrowse,
        isRemoteArchive: isRemoteArchive,
        onOpenPluginsSettings: onOpenPluginsSettings
      )
    } detail: {
      VStack(spacing: 0) {
        detailBanners
        GeometryReader { proxy in
          Group {
            switch destination {
            case .overview:
              ArchiveOverviewView(
                trawlers: trawlers,
                statusesByID: statusesByID,
                operabilityByID: effectiveOperabilityByID,
                hostDisplayName: hostDisplayName,
                freshnessLine: freshnessLine,
                isRemoteArchive: isRemoteArchive,
                hasCopiedAIPrompt: hasCopiedAIPrompt,
                onCopyAIPrompt: onCopyAIPrompt,
                onOpenAIAppsSettings: onOpenAIAppsSettings,
                onOpenOtherMacsSettings: onOpenOtherMacsSettings,
                onOpenHostSwitcher: onOpenHostSwitcher,
                onRunAttentionAction: onRunAttentionAction,
                onBrowseApp: { trawlerID in
                  interaction.query = ""
                  interaction.applyScopeAndDateRange(
                    registeredTrawler: trawlerID,
                    earliestInclusive: nil,
                    latestInclusive: nil
                  )
                  sidebarSelection = .app(trawlerID)
                  destination = .search
                },
                onBrowseAllItems: {
                  interaction.query = ""
                  interaction.applyScopeAndDateRange(
                    registeredTrawler: nil,
                    earliestInclusive: nil,
                    latestInclusive: nil
                  )
                  sidebarSelection = .allItems
                  destination = .search
                },
                onSelectDateRange: { trawlerID, start, end in
                  interaction.applyScopeAndDateRange(
                    registeredTrawler: trawlerID,
                    earliestInclusive: start,
                    latestInclusive: end
                  )
                  sidebarSelection = trawlerID.map { .app($0) } ?? .allItems
                  destination = .search
                }
              )
            case .search:
              searchBody(availableWidth: proxy.size.width)
            }
          }
          .frame(width: proxy.size.width, height: proxy.size.height)
          .onAppear { lastDetailWidth = proxy.size.width }
          .onChange(of: proxy.size.width) { _, newWidth in
            lastDetailWidth = newWidth
          }
        }
      }
      .navigationTitle(navigationTitleText)
    }
    .navigationSplitViewStyle(.balanced)
    .searchable(
      text: $interaction.query,
      isPresented: $isSearchPresented,
      placement: .toolbar,
      prompt: "Search your archive"
    )
    .toolbar {
      ToolbarItem(placement: .primaryAction) {
        dateFilterToolbarButton
      }
    }
    .onChange(of: sidebarSelection) { _, newSelection in
      guard let newSelection else { return }
      switch newSelection {
      case .overview:
        interaction.query = ""
        interaction.applyScopeAndDateRange(
          registeredTrawler: nil,
          earliestInclusive: nil,
          latestInclusive: nil
        )
        destination = .overview
      case .allItems:
        interaction.changeScope(to: nil)
        destination = .search
      case .app(let trawlerID):
        interaction.changeScope(to: trawlerID)
        destination = .search
      }
    }
    .onChange(of: interaction.query) { _, newValue in
      if !newValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && destination != .search {
        if sidebarSelection == .overview || sidebarSelection == nil {
          sidebarSelection = .allItems
        }
        destination = .search
      }
    }
    .onChange(of: model.phase) { _, newPhase in
      if newPhase != .loading {
        interaction.reconcileCommittedResults()
        if let committedInput = model.committedInput {
          let committedIsEmptyBrowse =
            committedInput.query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && committedInput.earliestMatchingArchiveRecordTimeInclusive == nil
            && committedInput.latestMatchingArchiveRecordTimeInclusive == nil
          if committedIsEmptyBrowse {
            for trawlerSearchResult in model.trawlerSearchResults {
              emptyBrowseCountByID[trawlerSearchResult.registeredTrawler] =
                trawlerSearchResult.totalSearchMatches
            }
          }
          if committedInput.registeredTrawler == nil {
            searchMatchCountsFromLastSearchOfAllApps = SearchMatchCountsForAllApps(
              query: committedInput.query.trimmingCharacters(in: .whitespacesAndNewlines),
              earliestDateInclusive: committedInput.earliestMatchingArchiveRecordTimeInclusive,
              latestDateInclusive: committedInput.latestMatchingArchiveRecordTimeInclusive,
              searchMatchCountByRegisteredTrawler: Dictionary(
                model.trawlerSearchResults.map {
                  (
                    $0.registeredTrawler,
                    AppSearchMatchCount(
                      searchMatchCount: $0.totalSearchMatches,
                      isLowerBound: $0.totalSearchMatchesIsLowerBound)
                  )
                },
                uniquingKeysWith: { _, latest in latest }
              ),
              totalSearchMatchCount: AppSearchMatchCount(
                searchMatchCount: model.totalMatchedRecordCount,
                isLowerBound: model.totalMatchedCountIsLowerBound))
          }
        }
        // Auto-open the first result on wide desktop layouts for both text search
        // and browsing; narrow layouts stay on the list so typing never kicks the
        // user out of their results.
        if lastDetailWidth >= 740,
          interaction.selectedSearchMatchIdentifier == nil,
          let first = model.searchMatches.first
        {
          interaction.selectedSearchMatchIdentifier = first.id
          Task { await model.open(first) }
        }
      }
    }
    .onChange(of: focusRequestToken) { _, _ in
      isSearchPresented = true
    }
    .onChange(of: trawlerStatuses) { _, statuses in
      trawlerResolver.replace(with: statuses)
      emptyBrowseCountByID = [:]
      searchMatchCountsFromLastSearchOfAllApps = nil
    }
    .onExitCommand(perform: handleEscape)
    .task(
      id: SearchTriggerKey(
        destination: destination,
        query: interaction.query,
        registeredTrawler: interaction.registeredTrawler,
        earliestDateInclusive: interaction.earliestMatchingArchiveRecordTimeInclusive,
        latestDateInclusive: interaction.latestMatchingArchiveRecordTimeInclusive,
        retryToken: searchRetryToken
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

  private var dateFilterToolbarButton: some View {
    Button {
      draftStartDate =
        interaction.earliestMatchingArchiveRecordTimeInclusive
        ?? Calendar.current.date(byAdding: .day, value: -30, to: Date())
        ?? Date()
      draftEndDate = interaction.latestMatchingArchiveRecordTimeInclusive ?? Date()
      isShowingDatePicker.toggle()
    } label: {
      Label(
        activeDateRangeLabel ?? "Dates",
        systemImage: "calendar"
      )
      .labelStyle(.titleAndIcon)
      .foregroundStyle(hasActiveDateRange ? TrawlDesign.Colors.matchRed : Color.primary)
    }
    .help("Choose dates")
    .popover(isPresented: $isShowingDatePicker, arrowEdge: .bottom) {
      VStack(alignment: .leading, spacing: TrawlDesign.Space.md) {
        Text("Choose dates")
          .font(TrawlDesign.Typography.sectionHeader)
        HStack(spacing: TrawlDesign.Space.xs) {
          quickDateRangeButton("7 days", inclusiveDays: 7)
          quickDateRangeButton("30 days", inclusiveDays: 30)
          quickDateRangeButton("1 year", inclusiveDays: 365)
        }
        Divider()
        DatePicker("From", selection: $draftStartDate, displayedComponents: .date)
        DatePicker("To", selection: $draftEndDate, displayedComponents: .date)
        HStack {
          if hasActiveDateRange {
            Button("Clear") {
              interaction.clearDateRange()
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
            interaction.changeDateRange(earliestInclusive: start, latestInclusive: max(start, end))
            if sidebarSelection == .overview || sidebarSelection == nil {
              sidebarSelection = .allItems
            }
            destination = .search
            isShowingDatePicker = false
          }
          .buttonStyle(.borderedProminent)
        }
      }
      .padding(TrawlDesign.Space.md)
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
      interaction.changeDateRange(earliestInclusive: start, latestInclusive: max(start, end))
      if sidebarSelection == .overview || sidebarSelection == nil {
        sidebarSelection = .allItems
      }
      destination = .search
      isShowingDatePicker = false
    }
    .buttonStyle(.bordered)
    .controlSize(.small)
  }

  private func searchBody(availableWidth: CGFloat) -> some View {
    let resultsColumnWidth = min(
      max(280, availableWidth * 0.40),
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
              timedOutLocally: model.timedOutLocally,
              onRetry: retryCurrentSearch,
              onOpenConnection: isRemoteArchive ? onOpenHostSwitcher : nil
            )
          }
        case .outcome:
          SearchOutcome(
            phase: model.phase,
            isScoped: selectedScopeTrawler != nil,
            timedOutLocally: model.timedOutLocally,
            onRetry: retryCurrentSearch,
            onOpenConnection: isRemoteArchive ? onOpenHostSwitcher : nil
          )
        case .results:
          if availableWidth < 740 {
            if model.openPhase == .idle {
              resultsListView
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
              VStack(spacing: 0) {
                HStack {
                  Button {
                    closeOpenedRecord()
                  } label: {
                    Label("Results", systemImage: "chevron.left")
                      .labelStyle(.titleAndIcon)
                  }
                  .buttonStyle(.bordered)
                  .controlSize(.small)
                  .fixedSize()
                  Spacer()
                }
                .padding(.horizontal, TrawlDesign.Space.md)
                .padding(.vertical, TrawlDesign.Space.sm)

                Divider()

                ResultPreview(
                  phase: model.openPhase,
                  highlightedTerms: highlightedTermsForOpenedRecord,
                  onOpenLink: openLinkedRecord
                )
              }
            }
          } else {
            HStack(spacing: 0) {
              resultsListView
                .frame(width: resultsColumnWidth)

              Divider()

              if model.openPhase == .idle {
                ContentUnavailableView(
                  "No Item Selected",
                  systemImage: "doc.text.magnifyingglass",
                  description: Text("Select a result from the list to read it.")
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
              } else {
                ResultPreview(
                  phase: model.openPhase,
                  highlightedTerms: highlightedTermsForOpenedRecord,
                  onOpenLink: openLinkedRecord
                )
              }
            }
          }
        }
      }
      .frame(maxWidth: .infinity, maxHeight: .infinity)

      SearchDateLineScrubberView(
        trawlerStatuses: trawlerStatuses,
        scopedTrawlerID: interaction.registeredTrawler,
        scopedTrawlerDisplayName: selectedScopeTrawler?.registeredTrawlerDisplayName,
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

  private var resultsListView: some View {
    SearchResultsList(
      phase: model.phase,
      searchMatches: model.searchMatches,
      totalMatchedRecordCount: model.totalMatchedRecordCount,
      totalMatchedCountIsLowerBound: model.totalMatchedCountIsLowerBound,
      isTruncated: model.isTruncated,
      isLoadingMore: model.isLoadingMore,
      canLoadMore: model.canLoadMore,
      loadMoreFailureMessage: model.loadMoreFailureMessage,
      appFilterChips: searchAppFilterChips,
      allAppsSearchMatchCountText: allAppsChipCountText,
      appFilterSelection: interaction.registeredTrawler,
      onSelectAppFilter: { registeredTrawler in
        sidebarSelection = registeredTrawler.map { .app($0) } ?? .allItems
      },
      activeDateRangeLabel: activeDateRangeLabel,
      onClearDateRange: { interaction.clearDateRange() },
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
  }

  private func retryCurrentSearch() {
    searchRetryToken &+= 1
  }

  private var highlightedTermsForOpenedRecord: [String] {
    var terms: [String] = []
    if let selectedID = interaction.selectedSearchMatchIdentifier,
      let selectedMatch = model.searchMatches.first(where: { $0.id == selectedID })
    {
      for field in selectedMatch.searchMatchPresentation.searchMatchTextFieldsInDisplayOrder {
        for fragment in field.searchMatchTextFragmentsInDisplayOrder
        where fragment.searchMatchTextFragmentMatchesSearchQuery {
          let trimmed = fragment.searchMatchTextFragmentContent
            .trimmingCharacters(in: .whitespacesAndNewlines)
          if !trimmed.isEmpty {
            terms.append(trimmed)
          }
        }
      }
    }
    if terms.isEmpty, let query = model.committedInput?.query {
      terms = query
        .split(whereSeparator: \.isWhitespace)
        .map { $0.trimmingCharacters(in: CharacterSet(charactersIn: "\"'")) }
        .filter { !$0.isEmpty }
    }
    return terms
  }

  private func closeOpenedRecord() {
    model.clearOpenResult()
    focus = .results
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

  private func openSelectedResult() {
    guard interaction.resultForReturn() != nil else { return }
    Task { await interaction.handleReturn() }
  }

  private func open(_ searchMatch: SearchMatch) {
    interaction.selectedSearchMatchIdentifier = searchMatch.id
    Task { await model.open(searchMatch) }
  }

  private func handleEscape() {
    if model.openPhase != .idle {
      model.clearOpenResult()
      focus = .results
    } else if !interaction.query.isEmpty {
      interaction.query = ""
    }
  }
}

struct SearchOutcome: View {
  let phase: SearchPhase
  let isScoped: Bool
  let timedOutLocally: Bool
  var onRetry: (() -> Void)? = nil
  var onOpenConnection: (() -> Void)? = nil

  private var showsRetryActions: Bool {
    switch phase {
    case .failed, .timedOut:
      true
    case .idle, .loading, .complete, .partial, .skipped:
      false
    }
  }

  @ViewBuilder
  var body: some View {
    switch phase {
    case .idle:
      ContentUnavailableView(
        "Search Your Archive",
        systemImage: "magnifyingglass",
        description: Text("Type words in the search field or choose dates to browse.")
      )
      .frame(maxWidth: .infinity, maxHeight: .infinity)
    case .loading:
      ProgressView("Searching…")
        .controlSize(.large)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    case .complete, .partial, .skipped, .failed, .timedOut:
      let detail = OperationalCopy.Search.outcomeDetail(
        for: phase,
        isScoped: isScoped,
        timedOutLocally: timedOutLocally,
        timeoutSeconds: SearchModel.defaultWaitSeconds
      )
      VStack(spacing: 14) {
        Image(systemName: phase == .complete ? "magnifyingglass" : "exclamationmark.circle")
          .font(TrawlDesign.Typography.pageTitle)
          .imageScale(.large)
          .foregroundStyle(.secondary)
        VStack(spacing: 6) {
          Text(OperationalCopy.Search.outcomeTitle(for: phase))
            .font(TrawlDesign.Typography.itemTitle)
            .foregroundStyle(.primary)
          Text(detail.isEmpty ? "Try different words or clear your filters." : detail)
            .font(TrawlDesign.Typography.body)
            .foregroundStyle(.secondary)
            .multilineTextAlignment(.center)
            .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: 440)
        if showsRetryActions && (onRetry != nil || onOpenConnection != nil) {
          HStack(spacing: 10) {
            if let onOpenConnection {
              Button(OperationalCopy.Connection.changeArchiveHost, action: onOpenConnection)
                .buttonStyle(.bordered)
                .fixedSize()
            }
            if let onRetry {
              Button(OperationalCopy.SharedAction.retry, action: onRetry)
                .buttonStyle(.borderedProminent)
                .fixedSize()
            }
          }
          .padding(.top, 4)
        }
      }
      .padding(24)
      .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
  }
}

private struct SearchMatchCountsForAllApps {
  let query: String
  let earliestDateInclusive: Date?
  let latestDateInclusive: Date?
  let searchMatchCountByRegisteredTrawler: [RegisteredTrawlerIdentity: AppSearchMatchCount]
  let totalSearchMatchCount: AppSearchMatchCount
}

/// How many records of one app match the search. A lower bound is shown with
/// a plus sign everywhere, so a count never looks more exact than it is.
struct AppSearchMatchCount: Equatable {
  let searchMatchCount: UInt64
  let isLowerBound: Bool

  var text: String {
    isLowerBound ? "\(searchMatchCount.formatted())+" : searchMatchCount.formatted()
  }
}

private struct SearchTriggerKey: Equatable {
  let destination: WorkspaceDestination
  let query: String
  let registeredTrawler: RegisteredTrawlerIdentity?
  let earliestDateInclusive: Date?
  let latestDateInclusive: Date?
  let retryToken: Int
}
