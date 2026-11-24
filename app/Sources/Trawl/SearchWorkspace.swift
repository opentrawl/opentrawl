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

struct ArchiveTimelineWorkspaceView: View {
  let client: any TrawlClient
  let trawlers: [RestingTrawler]
  let searchableTrawlers: [RestingTrawler]
  let trawlerStatuses: [TrawlerStatus]
  let detailOverrides: [RegisteredTrawlerIdentity: String]
  let updatingTrawlers: Set<RegisteredTrawlerIdentity>
  let focusRequestToken: Int

  private enum SearchRecordPresentationState: Equatable {
    case hidden
    case showingRecord
    case returnedToResults
  }

  @State private var model: SearchModel
  @State private var interaction: SearchInteraction
  @State private var trawlerResolver: SearchTrawlerResolver
  @State private var fieldState = SearchFieldState()
  @State private var recordPresentation: SearchRecordPresentationState = .hidden
  @FocusState private var focus: SearchFocus?

  init(
    client: any TrawlClient,
    trawlers: [RestingTrawler],
    searchableTrawlers: [RestingTrawler],
    trawlerStatuses: [TrawlerStatus],
    detailOverrides: [RegisteredTrawlerIdentity: String],
    updatingTrawlers: Set<RegisteredTrawlerIdentity>,
    focusRequestToken: Int = 0
  ) {
    self.client = client
    self.trawlers = trawlers
    self.searchableTrawlers = searchableTrawlers
    self.trawlerStatuses = trawlerStatuses
    self.detailOverrides = detailOverrides
    self.updatingTrawlers = updatingTrawlers
    self.focusRequestToken = focusRequestToken
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

  private var selectedScopeTrawler: RestingTrawler? {
    guard let id = interaction.registeredTrawler else { return nil }
    return trawlers.first(where: { $0.id == id })
  }

  private var isSearchActive: Bool {
    interaction.hasSearchCriteria || model.phase != .idle
  }

  var body: some View {
    GeometryReader { proxy in
      let isCompact = TrawlDesign.usesCompactSearchLayout(width: proxy.size.width)
      let searchBarHeight: CGFloat = 68
      let restingFooterHeight: CGFloat = 44
      let restingAvailableLanesHeight: CGFloat = max(
        180,
        proxy.size.height - searchBarHeight - TrawlDesign.timelineAxisHeight - restingFooterHeight - 4
      )
      let maxSearchLanesHeight: CGFloat? =
        isSearchActive
        ? max(
          112,
          proxy.size.height - searchBarHeight - TrawlDesign.timelineAxisHeight
            - TrawlDesign.timelineMinimumBottomSplitHeight - 8
        )
        : nil

      VStack(spacing: 0) {
        SearchField(
          query: $interaction.query,
          scope: selectedScopeTrawler,
          availableTrawlers: searchableTrawlers,
          earliestDateInclusive: interaction.earliestMatchingArchiveRecordTimeInclusive,
          latestDateInclusive: interaction.latestMatchingArchiveRecordTimeInclusive,
          focus: $focus,
          onSelectScope: { selectTrawlerScope($0?.id) },
          onClearScope: { selectTrawlerScope(nil) },
          onSetDateRange: { earliest, latest in
            interaction.changeDateRange(earliestInclusive: earliest, latestInclusive: latest)
          },
          onClearDateRange: { interaction.clearDateRange() },
          onSubmit: openSelectedResult,
          onMoveToResults: focusResults
        )
        .id(fieldState.identity)
        .padding(.horizontal, TrawlDesign.Space.md)
        .padding(.vertical, 12)
        .background(TrawlDesign.Colors.paper)

        Rectangle()
          .fill(TrawlDesign.Colors.hairline)
          .frame(height: 1)

        ArchiveTimelineBandView(
          trawlers: trawlers,
          statusesByID: statusesByID,
          detailOverrides: detailOverrides,
          updatingTrawlers: updatingTrawlers,
          isSearchActive: isSearchActive,
          isSearchLoading: model.phase == .loading,
          selectedTrawlerID: interaction.registeredTrawler,
          earliestDateInclusive: interaction.earliestMatchingArchiveRecordTimeInclusive,
          latestDateInclusive: interaction.latestMatchingArchiveRecordTimeInclusive,
          searchMatches: model.searchMatches,
          trawlerSearchResults: model.trawlerSearchResults,
          searchFailures: model.operationFailures,
          searchSkips: model.trawlersSkippedFromOperation,
          selectedMatchID: interaction.selectedSearchMatchIdentifier,
          restingAvailableLanesHeight: restingAvailableLanesHeight,
          maxSearchLanesHeight: maxSearchLanesHeight,
          onSelectTrawler: selectTrawlerScope,
          onBrushDateRange: { earliest, latest, trawlerID in
            interaction.applyScopeAndDateRange(
              registeredTrawler: trawlerID,
              earliestInclusive: earliest,
              latestInclusive: latest
            )
          },
          onSelectMatch: open
        )

        if isSearchActive {
          Rectangle()
            .fill(TrawlDesign.Colors.hairline)
            .frame(height: 1)
          bottomSearchArea(isCompact: isCompact)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
          restingFooterHint
        }
      }
      .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
      .background(TrawlDesign.Colors.paper)
    }
    .onChange(of: model.phase) { oldPhase, newPhase in
      if case .idle = oldPhase, case .loading = newPhase, focus == nil {
        fieldState.requestFocus()
      }
      if newPhase != .loading {
        interaction.reconcileCommittedResults()
        if interaction.selectedSearchMatchIdentifier == nil {
          recordPresentation = .hidden
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
    .onAppear {
      Task { @MainActor in
        focus = .field
      }
    }
    .task(
      id: SearchKey(
        query: interaction.query,
        registeredTrawler: interaction.registeredTrawler,
        earliestDateInclusive: interaction.earliestMatchingArchiveRecordTimeInclusive,
        latestDateInclusive: interaction.latestMatchingArchiveRecordTimeInclusive
      )
    ) {
      await model.search(
        interaction.query,
        registeredTrawler: interaction.registeredTrawler,
        earliestMatchingArchiveRecordTimeInclusive:
          interaction.earliestMatchingArchiveRecordTimeInclusive,
        latestMatchingArchiveRecordTimeInclusive:
          interaction.latestMatchingArchiveRecordTimeInclusive
      )
    }
  }

  private var restingFooterHint: some View {
    let measuredHistories = trawlerStatuses.compactMap(\.archiveMonthlyHistory)
    let totalArchived = measuredHistories.reduce(UInt64(0)) { sum, history in
      sum &+ history.totalMeasuredRecordCount
    }
    let measuredCount = measuredHistories.count
    return VStack(spacing: 0) {
      Spacer(minLength: TrawlDesign.Space.sm)
      HStack(spacing: TrawlDesign.Space.md) {
        if measuredCount > 0 {
          Text("\(totalArchived.formatted()) records across \(measuredCount) apps")
            .font(TrawlDesign.Typography.monoSmall)
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
        Spacer()
        Text("Click an app to scope, or drag across the timeline to browse a date range")
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
      }
      .padding(.horizontal, TrawlDesign.Space.md)
      .padding(.bottom, TrawlDesign.Space.md)
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }

  @ViewBuilder
  private func bottomSearchArea(isCompact: Bool) -> some View {
    switch SearchWorkspaceMode.resolve(
      phase: model.phase,
      resultCount: model.searchMatches.count
    ) {
    case .field:
      if SearchWorkspaceFieldContent.resolve(isScoped: selectedScopeTrawler != nil) == .scopedPrompt,
        let scope = selectedScopeTrawler
      {
        ScopedSearchPrompt(scope: scope)
      }
    case .outcome:
      SearchOutcome(
        phase: model.phase,
        isScoped: selectedScopeTrawler != nil,
        timedOutLocally: model.timedOutLocally
      )
    case .results:
      workspaceLayout(isCompact: isCompact)
    }
  }

  @ViewBuilder
  private func workspaceLayout(isCompact: Bool) -> some View {
    switch SearchWorkspaceLayout.resolve(
      isCompact: isCompact,
      showsCompactRecord: recordPresentation == .showingRecord,
      openPhase: model.openPhase
    ) {
    case .results:
      if isCompact {
        results(isCompact: true)
          .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
      } else {
        wideWorkspace(layout: .results, isCompact: false) {
          Rectangle()
            .fill(TrawlDesign.Colors.hairline)
            .frame(width: 1)
          VStack(spacing: 6) {
            Text("Select a result to open it")
              .font(TrawlDesign.Typography.body)
              .foregroundStyle(TrawlDesign.Colors.mutedInk)
            Text("Click a row on the left or a red diamond on the timeline.")
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(TrawlDesign.Colors.mutedInk.opacity(0.8))
          }
          .frame(maxWidth: .infinity, maxHeight: .infinity)
          .background(TrawlDesign.Colors.surface.opacity(0.45))
        }
      }
    case .compactRecord:
      ZStack {
        results(isCompact: true)
          .opacity(0)
          .allowsHitTesting(false)
          .accessibilityHidden(true)
        CompactRecordWorkspace(
          phase: model.openPhase,
          focus: $focus,
          onReturnToResults: returnToResults,
          onOpenLink: openLinkedRecord
        )
      }
    case .split:
      wideWorkspace(layout: .split, isCompact: false) {
        Rectangle()
          .fill(TrawlDesign.Colors.hairline)
          .frame(width: 1)
        ResultPreview(phase: model.openPhase, onOpenLink: openLinkedRecord)
      }
    }
  }

  private func wideWorkspace<Preview: View>(
    layout: SearchWorkspaceLayout,
    isCompact: Bool,
    @ViewBuilder preview: () -> Preview
  ) -> some View {
    let geometry = SearchWorkspaceGeometry.layout(for: layout)
    return HStack(spacing: 0) {
      results(isCompact: isCompact)
        .frame(
          minWidth: TrawlDesign.searchResultsMinimumWidth,
          idealWidth: SearchWorkspaceGeometry.wideResultsWidth,
          maxWidth: SearchWorkspaceGeometry.wideResultsWidth,
          maxHeight: .infinity
        )
      preview()
    }
    .padding(.leading, geometry.resultsOriginX)
    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
  }

  private func results(isCompact: Bool) -> some View {
    SearchResultsList(
      phase: model.phase,
      searchMatches: model.searchMatches,
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
      onSelectionChanged: { hit in
        if !isCompact { open(hit) }
      }
    )
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

  private func selectTrawlerScope(_ registeredTrawler: RegisteredTrawlerIdentity?) {
    interaction.changeScope(to: registeredTrawler)
    if !interaction.hasSearchCriteria {
      fieldState.requestFocus()
    }
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
    recordPresentation = .showingRecord
    Task { await interaction.handleReturn() }
  }

  private func open(_ searchMatch: SearchMatch) {
    interaction.selectedSearchMatchIdentifier = searchMatch.id
    recordPresentation = .showingRecord
    Task { await interaction.handleReturn() }
  }

  private func returnToResults() {
    recordPresentation = .returnedToResults
    focus = .results
  }

  private func handleEscape() {
    switch SearchEscapeAction.resolve(
      showsRecord: recordPresentation == .showingRecord
        || (model.openPhase != .idle && recordPresentation != .returnedToResults),
      focus: focus
    ) {
    case .closeRecord:
      model.clearOpenResult()
      recordPresentation = .hidden
      focus = interaction.selectedSearchMatchIdentifier == nil ? .field : .results
    case .focusField:
      focus = .field
    case .dismiss:
      if !interaction.query.isEmpty || interaction.hasActiveDateRange || interaction.registeredTrawler != nil {
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
}

private struct CompactRecordWorkspace: View {
  let phase: SearchOpenPhase
  @FocusState.Binding var focus: SearchFocus?
  let onReturnToResults: () -> Void
  let onOpenLink: (GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void

  var body: some View {
    VStack(spacing: 0) {
      HStack {
        Button(action: onReturnToResults) {
          Label("Results", systemImage: "chevron.left")
        }
        .buttonStyle(.borderless)
        .accessibilityLabel("Back to results")
        .focused($focus, equals: .record)
        Spacer()
      }
      .padding(.horizontal, 14)
      .padding(.vertical, 9)
      Divider()
      ResultPreview(phase: phase, onOpenLink: onOpenLink)
    }
    .onAppear { focus = .record }
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
    HStack(spacing: 10) {
      Image(systemName: "magnifyingglass")
        .font(.body.weight(.medium))
        .foregroundStyle(TrawlDesign.Colors.mutedInk)

      TextField(
        scope.map { "Search \($0.registeredTrawlerDisplayName)" }
          ?? "Find anything in your archive",
        text: $query
      )
      .font(.system(size: 15))
      .textFieldStyle(.plain)
      .focused($focus, equals: .field)
      .defaultFocus($focus, .field, priority: .userInitiated)
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
      scopeMenu

      if !query.isEmpty {
        Button(action: clearQuery) {
          Image(systemName: "xmark.circle.fill")
            .font(.body)
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
            .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .help("Clear search query")
        .accessibilityLabel("Clear search query")
      }
    }
    .padding(.horizontal, 14)
    .frame(height: 44)
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
      .padding(.horizontal, 10)
      .padding(.vertical, 5)
      .background(TrawlDesign.Colors.hairline.opacity(0.45), in: Capsule())
      .fixedSize()
    }
    .buttonStyle(.plain)
    .fixedSize(horizontal: true, vertical: false)
    .help("Choose an inclusive date range")
    .accessibilityLabel("Filter by date range")
    .popover(isPresented: $isShowingDatePicker, arrowEdge: .bottom) {
      VStack(alignment: .leading, spacing: 12) {
        Text("Date range")
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
    .padding(.leading, 10)
    .padding(.trailing, 7)
    .padding(.vertical, 4)
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
    HStack(spacing: 6) {
      TrawlerIconView(registeredTrawler: scope.id, size: 16)
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
    .padding(.leading, 8)
    .padding(.trailing, 7)
    .padding(.vertical, 4)
    .background(TrawlDesign.Colors.selectBlueSurface, in: Capsule())
    .overlay {
      Capsule()
        .stroke(TrawlDesign.Colors.selectBlue.opacity(0.32), lineWidth: 1)
    }
    .fixedSize()
  }

  @ViewBuilder
  private var scopeMenu: some View {
    if !availableTrawlers.isEmpty {
      Menu {
        Button {
          onClearScope()
        } label: {
          if scope == nil {
            Label("All apps", systemImage: "checkmark")
          } else {
            Text("All apps")
          }
        }
        Divider()
        ForEach(availableTrawlers) { trawler in
          Button {
            onSelectScope(trawler)
          } label: {
            if scope?.id == trawler.id {
              Label(trawler.registeredTrawlerDisplayName, systemImage: "checkmark")
            } else {
              Text(trawler.registeredTrawlerDisplayName)
            }
          }
        }
      } label: {
        HStack(spacing: 4) {
          Text(scope == nil ? "All apps" : "Switch app")
            .font(.callout.weight(.medium))
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
            .lineLimit(1)
            .fixedSize()
          Image(systemName: "chevron.up.chevron.down")
            .font(.caption2.weight(.semibold))
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(TrawlDesign.Colors.hairline.opacity(0.45), in: Capsule())
      }
      .menuStyle(.borderlessButton)
      .fixedSize(horizontal: true, vertical: false)
      .help("Choose which app to search")
      .accessibilityLabel(
        scope.map { "Searching \($0.registeredTrawlerDisplayName). Change app scope." }
          ?? "Searching all apps. Change app scope."
      )
    }
  }

  private func clearQuery() {
    query = ""
    Task { @MainActor in
      focus = .field
    }
  }
}

private struct ScopedSearchPrompt: View {
  let scope: RestingTrawler

  var body: some View {
    ContentUnavailableView {
      Label("Search \(scope.registeredTrawlerDisplayName)", systemImage: "magnifyingglass")
    } description: {
      Text("Enter a word or phrase, or drag across the timeline to browse this app.")
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
    .padding()
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
