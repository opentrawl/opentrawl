import AppKit
import PermissionGuide
import ServiceManagement
import SwiftUI
import TrawlClient
import TrawlCore

enum SettingsPane: String, CaseIterable, Identifiable {
  case general = "General"
  case apps = "Apps"
  case aiApps = "AI apps"
  case otherMacs = "Other Macs"
  case privacy = "Privacy"

  var id: String { rawValue }

  var systemImage: String {
    switch self {
    case .general: "gearshape"
    case .apps: "square.grid.2x2"
    case .aiApps: "sparkles"
    case .otherMacs: "laptopcomputer.and.iphone"
    case .privacy: "hand.raised"
    }
  }
}

/// Native macOS Settings window (⌘,) using standard toolbar tabs and grouped forms.
struct OpenTrawlSettingsView: View {
  @Environment(TrawlerIconStore.self) private var iconStore

  @Bindable var model: AppModel
  let connectionStore: ArchiveConnectionStore
  let remoteArchiveServer: RemoteArchiveServerController
  let runtimeConfiguration: TrawlRuntimeConfiguration
  let appInstallations: MacAppInstallations
  let buildIdentity: BuildIdentity
  let aiInstruction: String
  let personalWikiPrompt: String
  @Binding var selectedPane: SettingsPane
  @Binding var keepRunningInMenuBar: Bool
  let onConnectionChanged: () -> Void
  let onOpenFullDiskAccess: () -> Void
  let onSignInGmail: () -> Void
  let onSignOutGmail: () -> Void
  let onSignInX: () -> Void
  let onSignOutX: () -> Void
  let onOpenSourceApp: (String) -> Void

  @State private var copiedAIPrompt = false
  @State private var copiedPersonalWikiPrompt = false
  @State private var copiedMCPCommand = false
  @State private var copiedRemoteArchiveAddress = false
  @State private var copiedTokenCommand = false
  @State private var copiedAuditPrompt = false
  @State private var openAtLogin = SMAppService.mainApp.status == .enabled
  @State private var loginItemError: String?
  @State private var confirmingGmailSignOut = false
  @State private var confirmingXSignOut = false

  var body: some View {
    TabView(selection: $selectedPane) {
      generalPane
        .tabItem {
          Label(SettingsPane.general.rawValue, systemImage: SettingsPane.general.systemImage)
        }
        .tag(SettingsPane.general)

      appsPane
        .tabItem {
          Label(SettingsPane.apps.rawValue, systemImage: SettingsPane.apps.systemImage)
        }
        .tag(SettingsPane.apps)

      aiAppsPane
        .tabItem {
          Label(SettingsPane.aiApps.rawValue, systemImage: SettingsPane.aiApps.systemImage)
        }
        .tag(SettingsPane.aiApps)

      otherMacsPane
        .tabItem {
          Label(SettingsPane.otherMacs.rawValue, systemImage: SettingsPane.otherMacs.systemImage)
        }
        .tag(SettingsPane.otherMacs)

      privacyPane
        .tabItem {
          Label(SettingsPane.privacy.rawValue, systemImage: SettingsPane.privacy.systemImage)
        }
        .tag(SettingsPane.privacy)
    }
    .frame(width: 600, height: 500)
    .onAppear {
      openAtLogin = SMAppService.mainApp.status == .enabled
      _ = model.checkDiskAccess()
      iconStore.update(registeredTrawlerCatalog: model.registeredTrawlerCatalog)
    }
    .onChange(of: model.registeredTrawlerCatalog) { _, catalog in
      iconStore.update(registeredTrawlerCatalog: catalog)
    }
    .confirmationDialog(
      "Sign out of Gmail?",
      isPresented: $confirmingGmailSignOut,
      titleVisibility: .visible
    ) {
      Button("Sign out", role: .destructive, action: onSignOutGmail)
      Button("Cancel", role: .cancel) {}
    } message: {
      Text(
        "OpenTrawl stops getting new mail and removes its access at Google. Mail already copied stays in your archive."
      )
    }
    .confirmationDialog(
      "Sign out of X?",
      isPresented: $confirmingXSignOut,
      titleVisibility: .visible
    ) {
      Button("Sign out", role: .destructive, action: onSignOutX)
      Button("Cancel", role: .cancel) {}
    } message: {
      Text("OpenTrawl stops getting new posts. Earlier posts already copied stay in your archive.")
    }
  }

  // MARK: - General

  private var generalPane: some View {
    Form {
      Section("Background") {
        Toggle(isOn: $keepRunningInMenuBar) {
          VStack(alignment: .leading, spacing: 3) {
            Text("Keep OpenTrawl running in the menu bar")
              .font(TrawlDesign.Typography.body)
            Text(
              "When you close the window, OpenTrawl stays in the top-right menu bar so your archive stays up to date and your AI can still search."
            )
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)
          }
        }

        Toggle(
          isOn: Binding(
            get: { openAtLogin },
            set: { newValue in
              do {
                if newValue {
                  try SMAppService.mainApp.register()
                } else {
                  try SMAppService.mainApp.unregister()
                }
                openAtLogin = newValue
                loginItemError = nil
              } catch {
                loginItemError = error.localizedDescription
              }
            }
          )
        ) {
          VStack(alignment: .leading, spacing: 3) {
            Text("Open at login")
              .font(TrawlDesign.Typography.body)
            Text("AI apps on the web or your phone need this to reach this Mac after a restart.")
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(.secondary)
          }
        }

        if let loginItemError {
          Text(loginItemError)
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(TrawlDesign.Colors.matchRed)
        }
      }

      Section("Archive updates") {
        Text("OpenTrawl checks your apps automatically every hour while running on this Mac.")
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
      }
    }
    .formStyle(.grouped)
  }

  // MARK: - Apps

  private var appsPane: some View {
    let statusesByID = Dictionary(
      model.trawlerStatuses.map { ($0.id, $0) },
      uniquingKeysWith: { _, latest in latest }
    )
    let failuresByID = Dictionary(
      (model.statusOperationFailures + model.updateOperationFailures).map { ($0.failedTrawler, $0) },
      uniquingKeysWith: { _, latest in latest }
    )

    return Form {
      if connectionStore.isRemote {
        Section {
          Label(
            "You are searching the archive on \(connectionStore.activeHostShortDisplayName). Sign-ins and updates run on that Mac.",
            systemImage: "network"
          )
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
        }
      }

      Section("Built-in apps") {
        ForEach(model.homeTrawlers) { trawler in
          let state = AppOperabilityState.resolve(
            trawler: trawler,
            status: statusesByID[trawler.id],
            updateProgress: model.updateProgress[trawler.id],
            operationFailure: failuresByID[trawler.id],
            isRemoteArchive: connectionStore.isRemote,
            isInstalledOnThisMac: appInstallations.isAvailable(trawler.id),
            fullDiskAccessChoice: model.fullDiskAccessChoice
          )
          settingsAppRow(trawler: trawler, state: state, status: statusesByID[trawler.id])
        }
      }

      Section {
        Text(
          "Optional plugins (such as Money for bank and card exports, and X encrypted messages) can be installed through the OpenTrawl plugin catalog."
        )
        .font(TrawlDesign.Typography.meta)
        .foregroundStyle(.secondary)
      } header: {
        Text("Plugins")
      } footer: {
        Text("Plugins add more things to search. Add only plugins you trust.")
          .font(TrawlDesign.Typography.smallLabel)
      }
    }
    .formStyle(.grouped)
  }

  private func settingsAppRow(
    trawler: RestingTrawler,
    state: AppOperabilityState,
    status: TrawlerStatus?
  ) -> some View {
    let id = trawler.id.registeredTrawlerIdentity
    let isConnectedCloudAccount =
      !connectionStore.isRemote
      && (id == "gmail" || id == "twitter" || id == "x")
      && status?.archiveLiveSyncCredentialStatus == .available

    return HStack(spacing: 10) {
      TrawlerIconView(registeredTrawler: trawler.id, size: 22)
      VStack(alignment: .leading, spacing: 2) {
        Text(trawler.registeredTrawlerDisplayName)
          .font(TrawlDesign.Typography.bodyEmphasis)
        Text(state.statusLine)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(state.needsAttention ? TrawlDesign.Colors.warningAmberText : .secondary)
          .fixedSize(horizontal: false, vertical: true)
      }
      Spacer(minLength: 10)
      if let actionTitle = state.actionTitle, let action = state.action {
        Button(actionTitle) {
          perform(action, for: trawler.id)
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
      } else if isConnectedCloudAccount {
        Button("Sign out…") {
          if id == "gmail" {
            confirmingGmailSignOut = true
          } else {
            confirmingXSignOut = true
          }
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
      }
    }
  }

  private func perform(_ action: AppAttentionAction, for trawlerID: RegisteredTrawlerIdentity) {
    switch action {
    case .openFullDiskAccessSettings:
      onOpenFullDiskAccess()
    case .openPrivacySettings(let paneAnchor):
      if let url = URL(
        string: "x-apple.systempreferences:com.apple.preference.security?\(paneAnchor)"
      ) {
        NSWorkspace.shared.open(url)
      }
    case .openSourceApp(let bundleID, _):
      onOpenSourceApp(bundleID)
    case .signInGmail:
      onSignInGmail()
    case .signInX:
      onSignInX()
    case .retryUpdate:
      Task {
        await model.updateNow(registeredTrawlers: [trawlerID])
      }
    }
  }

  // MARK: - AI apps

  private var aiAppsPane: some View {
    let remoteMCPCommand = "\(runtimeConfiguration.agentCommand) mcp --http --tailscale"

    return Form {
      Section("AI apps on this Mac") {
        LabeledContent {
          Button(copiedAIPrompt ? "Copied" : HumanCopy.ConnectAI.copyAction) {
            copyToPasteboard(aiInstruction) { copiedAIPrompt = $0 }
          }
          .buttonStyle(.borderedProminent)
          .fixedSize()
        } label: {
          Text("Connect local AI apps")
            .font(TrawlDesign.Typography.body)
          Text("For Claude Code, Codex, Cursor, and other AI apps on this Mac.")
            .font(TrawlDesign.Typography.meta)
        }

        LabeledContent {
          Button(copiedPersonalWikiPrompt ? "Copied" : "Copy prompt") {
            copyToPasteboard(personalWikiPrompt) { copiedPersonalWikiPrompt = $0 }
          }
          .buttonStyle(.bordered)
          .fixedSize()
        } label: {
          Text("Build personal wiki")
            .font(TrawlDesign.Typography.body)
          Text("Copy a prompt for your AI to build a Markdown wiki in ~/.opentrawl/wiki/.")
            .font(TrawlDesign.Typography.meta)
        }
      }

      Section {
        VStack(alignment: .leading, spacing: 10) {
          Text(
            "For Claude, ChatGPT, Grok, and Meta Muse. OpenTrawl gives you a private web address to paste into the AI app. You approve each AI app on this Mac and choose which apps it can search."
          )
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
          .fixedSize(horizontal: false, vertical: true)

          Text("Run this command on this Mac to serve your archive over Tailscale:")
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)

          copyableCommandRow(
            command: remoteMCPCommand,
            isCopied: copiedMCPCommand,
            onCopy: { copyToPasteboard(remoteMCPCommand) { copiedMCPCommand = $0 } }
          )
        }
        .padding(.vertical, 2)
      } header: {
        Text("AI apps on the web or your phone")
      } footer: {
        Text("What an AI app reads is sent to the company that runs it. This Mac must be on and awake.")
          .font(TrawlDesign.Typography.smallLabel)
      }
    }
    .formStyle(.grouped)
  }

  // MARK: - Other Macs

  private var otherMacsPane: some View {
    let tokenCommand = "\(runtimeConfiguration.agentCommand) token create macbook --sources all"

    return Form {
      Section("This Mac searches") {
        RemoteArchiveConnectionView(
          connectionStore: connectionStore,
          showsLocalOption: true,
          showsHeader: false,
          isEmbedded: true,
          onConnectionChanged: onConnectionChanged,
          onDismiss: {}
        )
        .padding(.vertical, 4)
      }

      Section("Macs that can search this archive") {
        VStack(alignment: .leading, spacing: 10) {
          Toggle(
            isOn: Binding(
              get: { remoteArchiveServer.isSharingEnabled },
              set: { remoteArchiveServer.setSharingEnabled($0) }
            )
          ) {
            VStack(alignment: .leading, spacing: 3) {
              Text("Allow other Macs to search this archive over Tailscale")
                .font(TrawlDesign.Typography.body)
              Text(
                "Runs the read-only Tailscale archive server inside OpenTrawl on port \(RemoteArchiveServerController.defaultPortDisplay) (never Wi-Fi or LAN). Every request needs an access token."
              )
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(.secondary)
              .fixedSize(horizontal: false, vertical: true)
            }
          }
          .disabled(connectionStore.isRemote)

          remoteArchiveServerStatusView

          Divider()

          if remoteArchiveServer.activeLocalTokenCount > 0 {
            let count = remoteArchiveServer.activeLocalTokenCount
            Text(
              count == 1
                ? "1 active access token is configured on this Mac."
                : "\(count) active access tokens are configured on this Mac."
            )
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)
          }

          Text("Create an access token for another Mac:")
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)

          copyableCommandRow(
            command: tokenCommand,
            isCopied: copiedTokenCommand,
            onCopy: { copyToPasteboard(tokenCommand) { copiedTokenCommand = $0 } }
          )
        }
        .padding(.vertical, 2)
      }
    }
    .formStyle(.grouped)
    .onAppear {
      remoteArchiveServer.refreshLocalAccessTokenStatus()
    }
  }

  @ViewBuilder
  private var remoteArchiveServerStatusView: some View {
    switch remoteArchiveServer.state {
    case .running(let advertisedAddress):
      VStack(alignment: .leading, spacing: 6) {
        HStack(spacing: 6) {
          Image(systemName: "checkmark.circle.fill")
            .foregroundStyle(.green)
          Text("Sharing over Tailscale")
            .font(TrawlDesign.Typography.meta.weight(.medium))
            .foregroundStyle(TrawlDesign.Colors.ink)
        }
        copyableCommandRow(
          command: advertisedAddress,
          isCopied: copiedRemoteArchiveAddress,
          onCopy: {
            copyToPasteboard(advertisedAddress) { copiedRemoteArchiveAddress = $0 }
          }
        )
      }

    case .starting:
      HStack(spacing: 8) {
        ProgressView()
          .controlSize(.small)
        Text("Starting Tailscale archive server…")
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
      }

    case .portAlreadyInUse(let listenAddress):
      HStack(alignment: .center, spacing: 10) {
        Image(systemName: "exclamationmark.triangle.fill")
          .foregroundStyle(TrawlDesign.Colors.warningAmberText)
        Text(
          "Port \(RemoteArchiveServerController.defaultPortDisplay) (\(listenAddress)) is already in use by another process. Stop the other server and try again."
        )
        .font(TrawlDesign.Typography.meta)
        .foregroundStyle(TrawlDesign.Colors.warningAmberText)
        .fixedSize(horizontal: false, vertical: true)
        Spacer(minLength: 6)
        Button("Try again") {
          remoteArchiveServer.retryNow()
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
      }

    case .tailscaleUnavailable:
      HStack(alignment: .center, spacing: 10) {
        Image(systemName: "exclamationmark.triangle.fill")
          .foregroundStyle(TrawlDesign.Colors.warningAmberText)
        Text("Tailscale is not running on this Mac. Start Tailscale to share this archive.")
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.warningAmberText)
          .fixedSize(horizontal: false, vertical: true)
        Spacer(minLength: 6)
        Button("Try again") {
          remoteArchiveServer.retryNow()
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
      }

    case .failed(let message):
      HStack(alignment: .center, spacing: 10) {
        Image(systemName: "exclamationmark.circle.fill")
          .foregroundStyle(TrawlDesign.Colors.matchRed)
        Text(message)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.matchRed)
          .fixedSize(horizontal: false, vertical: true)
        Spacer(minLength: 6)
        Button("Try again") {
          remoteArchiveServer.retryNow()
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
      }

    case .disabledInRemoteMode:
      Text("Switch to This Mac above to share this Mac's archive.")
        .font(TrawlDesign.Typography.meta)
        .foregroundStyle(.secondary)

    case .stopped:
      EmptyView()
    }
  }

  // MARK: - Privacy

  private var privacyPane: some View {
    Form {
      Section("Full Disk Access") {
        LabeledContent {
          Button("Open System Settings", action: onOpenFullDiskAccess)
            .buttonStyle(.bordered)
            .fixedSize()
        } label: {
          Text(model.diskAccess == .granted ? "Allowed" : "Off")
            .font(TrawlDesign.Typography.body)
          Text(
            model.diskAccess == .granted
              ? "OpenTrawl can read Messages, Mail, Notes, WhatsApp, and Telegram."
              : "Turn it on in System Settings to include Messages, Mail, Notes, WhatsApp, and Telegram."
          )
          .font(TrawlDesign.Typography.meta)
        }
      }

      Section(HumanCopy.FullDiskAccess.trustGroupTitle) {
        VStack(alignment: .leading, spacing: 12) {
          Text(HumanCopy.FullDiskAccess.trustGroupBody)
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)

          HStack(spacing: 10) {
            Link(HumanCopy.FullDiskAccess.readCodeAction, destination: BuildIdentity.repositoryURL)
              .buttonStyle(.bordered)
              .fixedSize()
            Button(
              copiedAuditPrompt
                ? HumanCopy.FullDiskAccess.copiedAuditPromptAction
                : HumanCopy.FullDiskAccess.copyAuditPromptAction
            ) {
              copyToPasteboard(AgentPrompts.auditBuild(buildIdentity)) { copiedAuditPrompt = $0 }
            }
            .buttonStyle(.bordered)
            .fixedSize()
          }
        }
        .padding(.vertical, 2)
      }
    }
    .formStyle(.grouped)
  }

  private func copyableCommandRow(
    command: String,
    isCopied: Bool,
    onCopy: @escaping () -> Void
  ) -> some View {
    HStack(spacing: 10) {
      Text(command)
        .font(TrawlDesign.Typography.commandCode)
        .foregroundStyle(TrawlDesign.Colors.ink)
        .textSelection(.enabled)
        .frame(maxWidth: .infinity, alignment: .leading)
      Button(isCopied ? "Copied" : "Copy", action: onCopy)
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
    }
    .padding(.horizontal, 10)
    .padding(.vertical, 8)
    .background(TrawlDesign.Colors.surface, in: TrawlDesign.Shape.control)
    .overlay {
      TrawlDesign.Shape.control
        .strokeBorder(TrawlDesign.Colors.hairline, lineWidth: 1)
    }
  }

  private func copyToPasteboard(_ text: String, setCopied: @escaping (Bool) -> Void) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(text, forType: .string)
    setCopied(true)
    Task { @MainActor in
      try? await Task.sleep(for: .seconds(4))
      setCopied(false)
    }
  }
}
