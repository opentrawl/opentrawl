import AppKit
import SwiftUI
import TrawlClient
import TrawlCore

struct OnboardingView: View {
  let onboarding: OnboardingModel
  let appModel: AppModel
  let flags: AppFeatureFlags
  let onSearch: () -> Void

  private var syncAppIDs: [String] {
    flags.syncAppIDs(
      reportedAppIDs: appModel.sources.map(\.id)
        + appModel.statusFailures.map(\.sourceID)
        + appModel.skippedSources.map(\.sourceID)
    )
  }

  var body: some View {
    switch onboarding.stage {
    case .welcome:
      WelcomeStep(onContinue: onboarding.showTrust)
    case .trust:
      TrustStep(onContinue: {
        onboarding.requestPermission(appModel: appModel, appIDs: syncAppIDs)
      })
    case .permission:
      PermissionStep()
    case .syncing:
      SyncStep(
        appModel: appModel,
        flags: flags,
        onRetry: { onboarding.startInitialSync(appModel: appModel, appIDs: syncAppIDs) },
        onStop: onboarding.stopSync,
        onContinue: onboarding.showReady
      )
    case .ready:
      ReadyStep(onSearch: onSearch, onConnectAgent: onboarding.showAgent)
    case .agent:
      AgentStep(onBack: onboarding.showReady, onSearch: onSearch)
    case .complete:
      EmptyView()
    }
  }
}

private struct WelcomeStep: View {
  let onContinue: () -> Void

  var body: some View {
    OnboardingPage(step: OnboardingStrings.welcomeStep) {
      HStack(alignment: .top, spacing: 40) {
        VStack(alignment: .leading, spacing: 18) {
          Text(OnboardingStrings.welcomeTitle)
            .font(.largeTitle.bold())
          Text(OnboardingStrings.welcomeBody)
            .font(.title3)
            .foregroundStyle(.secondary)
            .frame(maxWidth: 540, alignment: .leading)
          Text(OnboardingStrings.archiveLocation)
            .font(.callout)
            .foregroundStyle(.secondary)
        }
        Spacer(minLength: 20)
        Image(nsImage: NSApplication.shared.applicationIconImage)
          .resizable()
          .scaledToFit()
          .frame(width: 124, height: 124)
      }
      OnboardingFacts()
      Button(OnboardingStrings.start, action: onContinue)
        .buttonStyle(.borderedProminent)
        .controlSize(.large)
    }
  }
}

private struct OnboardingFacts: View {
  var body: some View {
    HStack(alignment: .top, spacing: 0) {
      OnboardingFact(number: "01", text: OnboardingStrings.archiveStaysLocal)
      Divider()
      OnboardingFact(number: "02", text: OnboardingStrings.originalsStayUntouched)
      Divider()
      OnboardingFact(number: "03", text: OnboardingStrings.openSource)
    }
    .fixedSize(horizontal: false, vertical: true)
    .overlay(alignment: .top) {
      Rectangle().frame(height: 2)
    }
  }
}

private struct OnboardingFact: View {
  let number: String
  let text: String

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text(number)
        .font(.caption.bold())
        .foregroundStyle(TrawlDesign.brandRed)
      Text(text)
        .font(.body.weight(.semibold))
        .fixedSize(horizontal: false, vertical: true)
    }
    .frame(maxWidth: .infinity, alignment: .leading)
    .padding(.horizontal, 16)
    .padding(.vertical, 18)
  }
}

private struct TrustStep: View {
  @State private var copied = false
  let onContinue: () -> Void

  var body: some View {
    OnboardingPage(step: OnboardingStrings.trustStep) {
      Text(OnboardingStrings.trustTitle)
        .font(.largeTitle.bold())
      Text(OnboardingStrings.trustBody)
        .font(.title3)
        .foregroundStyle(.secondary)
        .frame(maxWidth: 680, alignment: .leading)
      Link(
        OnboardingStrings.codeLink,
        destination: URL(string: "https://github.com/opentrawl/opentrawl")!
      )
      .font(.headline)
      HStack {
        Button(copied ? OnboardingStrings.copied : OnboardingStrings.trustAction) {
          NSPasteboard.general.clearContents()
          NSPasteboard.general.setString(OnboardingStrings.auditPrompt, forType: .string)
          copied = true
        }
        Button(OnboardingStrings.trustContinue, action: onContinue)
          .buttonStyle(.borderedProminent)
      }
      .controlSize(.large)
    }
  }
}

private struct PermissionStep: View {
  var body: some View {
    OnboardingPage(step: OnboardingStrings.permissionStep) {
      ProgressView()
        .controlSize(.large)
      Text(OnboardingStrings.permissionTitle)
        .font(.title.bold())
      Text(OnboardingStrings.permissionBody)
        .font(.title3)
        .foregroundStyle(.secondary)
        .frame(maxWidth: 620, alignment: .leading)
      Text(OnboardingStrings.waitingForPermission)
        .foregroundStyle(.secondary)
    }
  }
}

private struct SyncStep: View {
  let appModel: AppModel
  let flags: AppFeatureFlags
  let onRetry: () -> Void
  let onStop: () -> Void
  let onContinue: () -> Void

  private var hasUsefulArchive: Bool {
    appModel.sources.contains { app in
      flags.includes(app.id)
        && app.counts.contains(where: { $0.value > 0 })
    }
  }

  var body: some View {
    OnboardingPage(step: OnboardingStrings.syncStep) {
      HStack {
        VStack(alignment: .leading, spacing: 6) {
          Text(OnboardingStrings.syncTitle)
            .font(.largeTitle.bold())
          Text(OnboardingStrings.syncBody)
            .font(.title3)
            .foregroundStyle(.secondary)
        }
        Spacer()
        if appModel.isSyncing {
          Button(OnboardingStrings.cancelSync, action: onStop)
        }
      }
      AppSyncList(appModel: appModel, flags: flags)
      HStack {
        if !appModel.isSyncing, !hasUsefulArchive || appModel.syncMessage != nil {
          Button(OnboardingStrings.retry, action: onRetry)
        }
        Spacer()
        if !appModel.isSyncing, hasUsefulArchive {
          Button(OnboardingStrings.continueToSearch, action: onContinue)
            .buttonStyle(.borderedProminent)
        }
      }
      .controlSize(.large)
    }
  }
}

private struct AppSyncList: View {
  let appModel: AppModel
  let flags: AppFeatureFlags

  private var appIDs: [String] {
    if let enabledAppIDs = flags.enabledAppIDs {
      return AppFeatureFlags.betaAppOrder.filter(enabledAppIDs.contains)
    }
    let reportedIDs =
      appModel.sources.map(\.id)
      + appModel.statusFailures.map(\.sourceID)
      + appModel.skippedSources.map(\.sourceID)
    return reportedIDs.reduce(into: []) { appIDs, appID in
      if !appIDs.contains(appID) { appIDs.append(appID) }
    }
  }

  var body: some View {
    VStack(spacing: 0) {
      ForEach(appIDs, id: \.self) { appID in
        AppSyncRow(
          name: status(appID)?.manifest.displayName ?? AppFeatureFlags.displayName(for: appID),
          counts: status(appID)?.counts ?? [],
          detail: detail(appID),
          progress: progress(appID)
        )
        Divider()
      }
      if flags.enabledAppIDs != nil {
        ForEach(AppFeatureFlags.comingSoonAppOrder, id: \.self) { appID in
          ComingSoonRow(appID: appID)
          Divider()
        }
      }
    }
    .overlay(alignment: .top) {
      Rectangle().frame(height: 2)
    }
    .overlay(alignment: .bottom) {
      Rectangle().frame(height: 1)
    }
  }

  private func status(_ appID: String) -> SourceStatus? {
    appModel.sources.first { $0.id == appID }
  }

  private func detail(_ appID: String) -> String? {
    if let failure = appModel.syncFailures.first(where: { $0.sourceID == appID }) {
      return failure.message
    }
    if let resultFailure = appModel.syncResults.first(where: { $0.sourceID == appID })?.failure {
      return resultFailure.message
    }
    if let failure = appModel.statusFailures.first(where: { $0.sourceID == appID }) {
      return failure.message
    }
    if let skipped = appModel.skippedSources.first(where: { $0.sourceID == appID }) {
      return skipped.reason
    }
    guard let status = status(appID) else { return nil }
    return status.setupRequirements.first(where: { $0.state == .needsAction })?.explanation
      ?? status.errors.first
      ?? status.warnings.first
  }

  private func progress(_ appID: String) -> AppSyncProgressState? {
    if let progress = appModel.syncProgress[appID] { return progress }
    if let failure = appModel.statusFailures.first(where: { $0.sourceID == appID }) {
      return .failed(failure.message)
    }
    if let skipped = appModel.skippedSources.first(where: { $0.sourceID == appID }) {
      return .failed(skipped.reason)
    }
    return nil
  }
}

private struct AppSyncRow: View {
  let name: String
  let counts: [SourceCount]
  let detail: String?
  let progress: AppSyncProgressState?

  var body: some View {
    HStack(spacing: 12) {
      VStack(alignment: .leading, spacing: 3) {
        HStack {
          Text(name)
            .font(.headline)
          if !counts.isEmpty {
            Text(
              OnboardingStrings.counts(
                counts.map { "\($0.value.formatted()) \($0.label.lowercased())" })
            )
            .foregroundStyle(.secondary)
          }
        }
        if let detail, !detail.isEmpty {
          Text(detail)
            .font(.callout)
            .foregroundStyle(.secondary)
        }
      }
      Spacer()
      Text(progressLabel)
        .foregroundStyle(progressIsFailure ? .red : .secondary)
    }
    .padding(.horizontal, 16)
    .frame(minHeight: 52)
  }

  private var progressLabel: String {
    switch progress {
    case .running: OnboardingStrings.syncing
    case .finished: OnboardingStrings.finished
    case .failed: OnboardingStrings.syncFailed
    case .waiting, nil: OnboardingStrings.waiting
    }
  }

  private var progressIsFailure: Bool {
    if case .failed = progress { true } else { false }
  }
}

private struct ComingSoonRow: View {
  let appID: String

  var body: some View {
    HStack {
      Text(appName)
        .font(.headline)
      Spacer()
      Text(OnboardingStrings.comingSoon)
        .foregroundStyle(.secondary)
    }
    .padding(.horizontal, 16)
    .frame(minHeight: 52)
  }

  private var appName: String {
    AppFeatureFlags.displayName(for: appID)
  }
}

private struct ReadyStep: View {
  let onSearch: () -> Void
  let onConnectAgent: () -> Void

  var body: some View {
    OnboardingPage(step: OnboardingStrings.readyStep) {
      Text(OnboardingStrings.readyTitle)
        .font(.largeTitle.bold())
      Text(OnboardingStrings.readyBody)
        .font(.title3)
        .foregroundStyle(.secondary)
      HStack {
        Button(OnboardingStrings.search, action: onSearch)
          .buttonStyle(.borderedProminent)
        Button(OnboardingStrings.connectAgent, action: onConnectAgent)
      }
      .controlSize(.large)
    }
  }
}

private struct AgentStep: View {
  @State private var copied = false
  let onBack: () -> Void
  let onSearch: () -> Void

  var body: some View {
    OnboardingPage(step: OnboardingStrings.agentStep) {
      Text(OnboardingStrings.agentTitle)
        .font(.largeTitle.bold())
      Text(OnboardingStrings.agentBody)
        .font(.title3)
        .foregroundStyle(.secondary)
      Text(OnboardingStrings.agentInstruction)
        .font(.system(.body, design: .monospaced))
        .textSelection(.enabled)
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.primary.opacity(0.055))
        .overlay {
          Rectangle().stroke(Color.primary, lineWidth: 1)
        }
      Text(OnboardingStrings.agentDoesNotInstall)
        .foregroundStyle(.secondary)
      HStack {
        Button(copied ? OnboardingStrings.copied : OnboardingStrings.copyAgentInstruction) {
          NSPasteboard.general.clearContents()
          NSPasteboard.general.setString(OnboardingStrings.agentInstruction, forType: .string)
          copied = true
        }
        .buttonStyle(.borderedProminent)
        Button(OnboardingStrings.search, action: onSearch)
        Button(OnboardingStrings.back, action: onBack)
      }
      .controlSize(.large)
    }
  }
}

private struct OnboardingPage<Content: View>: View {
  let step: String
  @ViewBuilder let content: Content

  init(
    step: String,
    @ViewBuilder content: () -> Content
  ) {
    self.step = step
    self.content = content()
  }

  var body: some View {
    VStack(spacing: 0) {
      OnboardingBrandRail(step: step)
      Rectangle()
        .frame(height: 2)
      VStack(alignment: .leading, spacing: 24) {
        content
      }
      .frame(maxWidth: 760, maxHeight: .infinity, alignment: .leading)
      .padding(.horizontal, 48)
      .padding(.vertical, 40)
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
    .tint(TrawlDesign.brandRed)
    .buttonBorderShape(.roundedRectangle(radius: 4))
  }
}

private struct OnboardingBrandRail: View {
  let step: String

  var body: some View {
    HStack(alignment: .firstTextBaseline) {
      HStack(spacing: 0) {
        Text("open")
          .foregroundStyle(.primary)
        Text("trawl")
          .foregroundStyle(TrawlDesign.brandRed)
      }
      .font(.body.bold())
      .tracking(-0.2)
      Spacer()
      Text(step)
        .font(.caption.bold())
        .tracking(1.1)
        .foregroundStyle(TrawlDesign.brandRed)
    }
    .padding(.horizontal, 32)
    .frame(height: 54)
  }
}
