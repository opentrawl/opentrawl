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

enum SearchWorkspaceFieldContent: Equatable {
  case none
  case scopedPrompt

  static func resolve(isScoped: Bool) -> Self {
    isScoped ? .scopedPrompt : .none
  }
}

struct SpotlightSearchHeader: View {
  @Binding var query: String
  let scope: RestingTrawler?
  let activeCategory: TrawlerFilterCategory
  let availableCategories: [TrawlerFilterCategory]
  let isSearching: Bool
  let fieldIdentity: UUID
  @FocusState.Binding var focus: SearchFocus?
  let onClearScope: () -> Void
  let onSelectCategory: (TrawlerFilterCategory) -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 10) {
      SpotlightSearchField(
        query: $query,
        scope: scope,
        isSearching: isSearching,
        focus: $focus,
        onClearScope: onClearScope,
        onSubmit: onSubmit,
        onMoveToResults: onMoveToResults
      )
      .id(fieldIdentity)

      if scope == nil, availableCategories.count > 1 {
        SpotlightCategoryChipBar(
          categories: availableCategories,
          activeCategory: activeCategory,
          onSelect: onSelectCategory
        )
      }
    }
    .padding(.horizontal, 18)
    .padding(.vertical, 12)
    .background(TrawlDesign.canvasBackground)
  }
}

private struct SpotlightSearchField: View {
  @Binding var query: String
  let scope: RestingTrawler?
  let isSearching: Bool
  @FocusState.Binding var focus: SearchFocus?
  let onClearScope: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void

  private var isFieldFocused: Bool {
    focus == .field
  }

  var body: some View {
    HStack(spacing: 10) {
      Group {
        if isSearching {
          ProgressView()
            .controlSize(.small)
            .frame(width: 16, height: 16)
        } else {
          Image(systemName: "magnifyingglass")
            .font(.system(size: 14, weight: .medium))
            .foregroundStyle(isFieldFocused ? TrawlDesign.brandRed : .secondary)
        }
      }
      .frame(width: 18)

      if let scope {
        HStack(spacing: 6) {
          TrawlerIconView(registeredTrawler: scope.id, size: 18)
          Text(scope.registeredTrawlerDisplayName)
            .font(.callout.weight(.semibold))
            .lineLimit(1)
          Button(action: onClearScope) {
            Image(systemName: "xmark")
              .font(.system(size: 9, weight: .bold))
              .foregroundStyle(.secondary)
          }
          .buttonStyle(.plain)
          .help("Search all apps")
          .accessibilityLabel("Clear app filter")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(TrawlDesign.brandRed.opacity(0.12), in: Capsule())
        .overlay(Capsule().strokeBorder(TrawlDesign.brandRed.opacity(0.28), lineWidth: 0.75))
        .fixedSize(horizontal: true, vertical: false)
      }

      TextField(
        scope.map { "Search \($0.registeredTrawlerDisplayName)…" }
          ?? OperationalCopy.Home.searchPlaceholder,
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

      if query.isEmpty {
        Text("⌘K")
          .font(.caption2.weight(.semibold).monospaced())
          .foregroundStyle(.secondary)
          .fixedSize()
          .padding(.horizontal, 6)
          .padding(.vertical, 3)
          .background(TrawlDesign.surfaceBackground, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
          .overlay(
            RoundedRectangle(cornerRadius: 5, style: .continuous)
              .strokeBorder(TrawlDesign.hairlineBorder, lineWidth: 0.5)
          )
          .fixedSize()
      } else {
        Button {
          query = ""
          Task { @MainActor in focus = .field }
        } label: {
          Image(systemName: "xmark.circle.fill")
            .font(.body)
            .foregroundStyle(.secondary)
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
      TrawlDesign.elevatedSurface,
      in: RoundedRectangle(cornerRadius: TrawlDesign.searchBarCornerRadius, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: TrawlDesign.searchBarCornerRadius, style: .continuous)
        .strokeBorder(
          isFieldFocused ? TrawlDesign.brandRed.opacity(0.55) : TrawlDesign.hairlineBorder,
          lineWidth: isFieldFocused ? 1.5 : 0.75
        )
    )
  }
}

private struct SpotlightCategoryChipBar: View {
  let categories: [TrawlerFilterCategory]
  let activeCategory: TrawlerFilterCategory
  let onSelect: (TrawlerFilterCategory) -> Void

  var body: some View {
    ScrollView(.horizontal, showsIndicators: false) {
      HStack(spacing: 6) {
        ForEach(categories) { category in
          let isSelected = category == activeCategory
          Button {
            onSelect(category)
          } label: {
            HStack(spacing: 5) {
              Image(systemName: category.symbolName)
                .font(.caption2.weight(.semibold))
              Text(category.displayName)
                .font(.caption.weight(isSelected ? .semibold : .medium))
            }
            .foregroundStyle(isSelected ? Color.white : .secondary)
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(
              isSelected ? TrawlDesign.brandRed : TrawlDesign.surfaceBackground,
              in: Capsule()
            )
            .overlay(
              Capsule().strokeBorder(
                isSelected ? Color.clear : TrawlDesign.hairlineBorder.opacity(0.8),
                lineWidth: 0.5
              )
            )
          }
          .buttonStyle(.plain)
          .accessibilityAddTraits(isSelected ? .isSelected : [])
        }
      }
    }
  }
}

struct SearchResultsWorkspace: View {
  @Bindable var interaction: SearchInteraction
  let scope: RestingTrawler?
  let activeCategory: TrawlerFilterCategory
  let filteredMatches: [SearchMatch]
  let trawlerResolver: SearchTrawlerResolver
  let isCompact: Bool
  let model: SearchModel
  let helperController: TrawlHelperController
  @FocusState.Binding var focus: SearchFocus?
  let onSubmit: () -> Void
  let onEscape: () -> Void
  let onOpen: (SearchMatch) -> Void
  let onOpenLink: (GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void
  let onReturnToResults: () -> Void
  let showsCompactRecord: Bool

  var body: some View {
    switch SearchWorkspaceMode.resolve(
      phase: model.phase,
      resultCount: filteredMatches.count
    ) {
    case .field, .outcome:
      SearchOutcomeView(
        phase: model.phase,
        isScoped: scope != nil || activeCategory != .all,
        timedOutLocally: model.timedOutLocally
      )
    case .results:
      switch SearchWorkspaceLayout.resolve(
        isCompact: isCompact,
        showsCompactRecord: showsCompactRecord,
        openPhase: model.openPhase
      ) {
      case .results:
        if isCompact {
          resultsList
        } else {
          HStack(spacing: 0) {
            resultsList
              .frame(width: TrawlDesign.searchResultsMaximumWidth)
            Divider()
            ResultPreview(
              phase: model.openPhase,
              trawlerDisplayName: { trawlerDisplayName(for: $0) },
              helperController: helperController,
              onOpenLink: onOpenLink
            )
          }
        }
      case .compactRecord:
        CompactRecordWorkspace(
          phase: model.openPhase,
          trawlerDisplayName: { trawlerDisplayName(for: $0) },
          helperController: helperController,
          focus: $focus,
          onReturnToResults: onReturnToResults,
          onOpenLink: onOpenLink
        )
      case .split:
        HStack(spacing: 0) {
          resultsList
            .frame(width: TrawlDesign.searchResultsMaximumWidth)
          Divider()
          ResultPreview(
            phase: model.openPhase,
            trawlerDisplayName: { trawlerDisplayName(for: $0) },
            helperController: helperController,
            onOpenLink: onOpenLink
          )
        }
      }
    }
  }

  private var resultsList: some View {
    SearchResultsList(
      phase: model.phase,
      searchMatches: filteredMatches,
      trawlerDisplayName: {
        $0.map(trawlerDisplayName(for:))
          ?? SearchTrawlerResolver.unavailableDisplayName
      },
      showsTrawlerDisplayName: scope == nil,
      committedQuery: model.committedInput?.query,
      resultLimit: model.resultLimit,
      title: model.displayTitle(for:),
      selectedSearchMatchIdentifier: $interaction.selectedSearchMatchIdentifier,
      focus: $focus,
      onReturn: onSubmit,
      onEscape: onEscape,
      onOpen: onOpen,
      onSelectionChanged: { hit in
        if !isCompact { onOpen(hit) }
      }
    )
  }

  private func trawlerDisplayName(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> String {
    if registeredTrawler == scope?.id {
      return scope?.registeredTrawlerDisplayName
        ?? SearchTrawlerResolver.unavailableDisplayName
    }
    return model.trawlerDisplayName(
      for: registeredTrawler,
      resolvedName: trawlerResolver.displayName(for: registeredTrawler)
    )
  }
}

private struct CompactRecordWorkspace: View {
  let phase: SearchOpenPhase
  let trawlerDisplayName: (RegisteredTrawlerIdentity) -> String?
  let helperController: TrawlHelperController
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
      ResultPreview(
        phase: phase,
        trawlerDisplayName: trawlerDisplayName,
        helperController: helperController,
        onOpenLink: onOpenLink
      )
    }
    .onAppear { focus = .record }
  }
}

private struct SearchOutcomeView: View {
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
}
