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

/// Native macOS Settings window (⌘,) with exact pane titles and plain-English,
/// zero-jargon controls for menu-bar/headless operation, built-in apps, AI access,
/// other Macs, and privacy.
struct OpenTrawlSettingsView: View {
  @Bindable var model: AppModel
  let connectionStore: ArchiveConnectionStore
  let runtimeConfiguration: TrawlRuntimeConfiguration
  let appInstallations: MacAppInstallations
  let buildIdentity: BuildIdentity
  let aiInstruction: String
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
  @State private var copiedAuditPrompt = false
  @State private var openAtLogin = SMAppService.mainApp.status == .enabled
  @State private var loginItemError: String?
  @State private var confirmingGmailSignOut = false
  @State private var confirmingXSignOut = false

  var body: some View {
    NavigationSplitView {
      List(SettingsPane.allCases, selection: $selectedPane) { pane in
        Label(pane.rawValue, systemImage: pane.systemImage)
          .tag(pane)
      }
      .listStyle(.sidebar)
      .navigationSplitViewColumnWidth(min: 165, ideal: 175, max: 200)
    } detail: {
      ScrollView {
        VStack(alignment: .leading, spacing: 18) {
          switch selectedPane {
          case .general:
            generalPane
          case .apps:
            appsPane
          case .aiApps:
            aiAppsPane
          case .otherMacs:
            otherMacsPane
          case .privacy:
            privacyPane
          }
        }
        .padding(22)
        .frame(maxWidth: 640, alignment: .leading)
        .frame(maxWidth: .infinity, alignment: .topLeading)
      }
      .background(TrawlDesign.Colors.paper)
      .navigationTitle(selectedPane.rawValue)
    }
    .frame(minWidth: 740, minHeight: 540)
    .onAppear {
      openAtLogin = SMAppService.mainApp.status == .enabled
      _ = model.checkDiskAccess()
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
    VStack(alignment: .leading, spacing: 16) {
      Text("General")
        .font(.title3.weight(.semibold))

      GroupBox {
        VStack(alignment: .leading, spacing: 14) {
          Toggle(isOn: $keepRunningInMenuBar) {
            VStack(alignment: .leading, spacing: 3) {
              Text("Keep OpenTrawl running in the menu bar")
                .font(.body.weight(.medium))
              Text(
                "When you close the window, OpenTrawl stays in the top-right menu bar. It keeps your archive up to date and your AI can still search. Quit from the menu bar icon."
              )
              .font(.caption)
              .foregroundStyle(.secondary)
            }
          }

          Divider()

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
                .font(.body.weight(.medium))
              Text("AI apps on the web or your phone need this to reach this Mac after a restart.")
                .font(.caption)
                .foregroundStyle(.secondary)
            }
          }

          if let loginItemError {
            Text(loginItemError)
              .font(.caption)
              .foregroundStyle(TrawlDesign.Colors.matchRed)
          }
        }
        .padding(6)
      }

      GroupBox {
        VStack(alignment: .leading, spacing: 6) {
          Text("Archive updates")
            .font(.body.weight(.medium))
          Text("OpenTrawl checks your apps automatically every hour while running on this Mac.")
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(6)
      }
    }
  }

  // MARK: - Apps

  private var appsPane: some View {
    let statusesByID = Dictionary(uniqueKeysWithValues: model.trawlerStatuses.map { ($0.id, $0) })
    let failuresByID = Dictionary(
      (model.statusOperationFailures + model.updateOperationFailures).map { ($0.failedTrawler, $0) },
      uniquingKeysWith: { _, latest in latest }
    )

    return VStack(alignment: .leading, spacing: 16) {
      Text("Built-in apps")
        .font(.title3.weight(.semibold))

      if connectionStore.isRemote {
        Text(
          "You are searching the archive on \(connectionStore.activeHostDisplayName). Sign-ins and updates run on that Mac."
        )
        .font(.callout)
        .foregroundStyle(.secondary)
      }

      VStack(spacing: 0) {
        ForEach(Array(model.homeTrawlers.enumerated()), id: \.element.id) { idx, trawler in
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
          if idx < model.homeTrawlers.count - 1 {
            Divider()
          }
        }
      }
      .background(
        TrawlDesign.Colors.paper,
        in: RoundedRectangle(cornerRadius: 10, style: .continuous)
      )
      .overlay(
        RoundedRectangle(cornerRadius: 10, style: .continuous)
          .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
      )

      VStack(alignment: .leading, spacing: 6) {
        Text("Plugins")
          .font(.headline)
        Text("Plugins add more things to search. Add only plugins you trust.")
          .font(.caption)
          .foregroundStyle(.secondary)
        Text(
          "Optional plugins (such as Money for bank and card exports, and X encrypted messages) can be installed through the OpenTrawl plugin catalog."
        )
        .font(.callout)
        .foregroundStyle(.secondary)
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
          TrawlDesign.Colors.surface,
          in: RoundedRectangle(cornerRadius: 10, style: .continuous)
        )
      }
    }
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
          .font(.system(size: 13, weight: .semibold))
        Text(state.statusLine)
          .font(.caption)
          .foregroundStyle(state.needsAttention ? TrawlDesign.Colors.warningAmber : .secondary)
          .fixedSize(horizontal: false, vertical: true)
      }
      Spacer(minLength: 10)
      if let actionTitle = state.actionTitle, let action = state.action {
        Button(actionTitle) {
          perform(action, for: trawler.id)
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
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
      }
    }
    .padding(.horizontal, 12)
    .padding(.vertical, 10)
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
    VStack(alignment: .leading, spacing: 16) {
      Text("AI apps")
        .font(.title3.weight(.semibold))

      GroupBox {
        HStack(alignment: .center, spacing: 12) {
          VStack(alignment: .leading, spacing: 4) {
            Text("AI apps on this Mac")
              .font(.body.weight(.semibold))
            Text("For Claude Code, Codex, Cursor, and other AI apps that run on this Mac.")
              .font(.caption)
              .foregroundStyle(.secondary)
          }
          Spacer()
          Button(copiedAIPrompt ? "Copied" : HumanCopy.ConnectAI.copyAction) {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(aiInstruction, forType: .string)
            copiedAIPrompt = true
            Task { @MainActor in
              try? await Task.sleep(for: .seconds(4))
              copiedAIPrompt = false
            }
          }
          .buttonStyle(.borderedProminent)
        }
        .padding(6)
      }

      GroupBox {
        VStack(alignment: .leading, spacing: 8) {
          Text("AI apps on the web or your phone")
            .font(.body.weight(.semibold))
          Text(
            "For Claude, ChatGPT, Grok, and Meta Muse. OpenTrawl gives you a private web address to paste into the AI app. You approve each AI app on this Mac and choose which apps it can search."
          )
          .font(.callout)
          .foregroundStyle(.secondary)
          Text(
            "What an AI app reads is sent to the company that runs it. This Mac must be on and awake."
          )
          .font(.caption.weight(.medium))
          .foregroundStyle(TrawlDesign.Colors.ink)
          Text("Run `trawl mcp --http --tailscale` on this Mac to serve your archive over Tailscale.")
            .font(TrawlDesign.Typography.monoSmall)
            .foregroundStyle(.secondary)
            .textSelection(.enabled)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(6)
      }
    }
  }

  // MARK: - Other Macs

  private var otherMacsPane: some View {
    VStack(alignment: .leading, spacing: 16) {
      Text("Other Macs")
        .font(.title3.weight(.semibold))

      GroupBox {
        VStack(alignment: .leading, spacing: 10) {
          Text("This Mac searches")
            .font(.body.weight(.semibold))
          RemoteArchiveConnectionView(
            connectionStore: connectionStore,
            showsLocalOption: true,
            onConnectionChanged: onConnectionChanged,
            onDismiss: {}
          )
        }
        .padding(4)
      }

      GroupBox {
        VStack(alignment: .leading, spacing: 6) {
          Text("Macs that can search this archive")
            .font(.body.weight(.semibold))
          Text(
            "To let another Mac search this Mac's archive read-only over Tailscale, create an access token on this Mac:"
          )
          .font(.caption)
          .foregroundStyle(.secondary)
          Text("\(runtimeConfiguration.agentCommand) token create macbook --sources all")
            .font(TrawlDesign.Typography.monoSmall)
            .textSelection(.enabled)
            .padding(8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(TrawlDesign.Colors.surface, in: RoundedRectangle(cornerRadius: 6))
        }
        .padding(6)
      }
    }
  }

  // MARK: - Privacy

  private var privacyPane: some View {
    VStack(alignment: .leading, spacing: 16) {
      Text("Privacy")
        .font(.title3.weight(.semibold))

      GroupBox {
        HStack(alignment: .center, spacing: 12) {
          VStack(alignment: .leading, spacing: 4) {
            Text("Full Disk Access")
              .font(.body.weight(.semibold))
            Text(
              model.diskAccess == .granted
                ? "On. OpenTrawl can read Messages, Mail, Notes, WhatsApp, and Telegram."
                : "Off. Turn it on in System Settings to include Messages, Mail, Notes, WhatsApp, and Telegram."
            )
            .font(.caption)
            .foregroundStyle(.secondary)
          }
          Spacer()
          Button("Open System Settings", action: onOpenFullDiskAccess)
            .buttonStyle(.bordered)
        }
        .padding(6)
      }

      GroupBox {
        VStack(alignment: .leading, spacing: 10) {
          Text(HumanCopy.FullDiskAccess.trustGroupTitle)
            .font(.body.weight(.semibold))
          Text(HumanCopy.FullDiskAccess.trustGroupBody)
            .font(.callout)
            .foregroundStyle(.secondary)
          HStack(spacing: 10) {
            if let githubURL = URL(string: "https://github.com/opentrawl/opentrawl") {
              Link(HumanCopy.FullDiskAccess.readCodeAction, destination: githubURL)
                .buttonStyle(.bordered)
            }
            Button(
              copiedAuditPrompt
                ? HumanCopy.FullDiskAccess.copiedAuditPromptAction
                : HumanCopy.FullDiskAccess.copyAuditPromptAction
            ) {
              NSPasteboard.general.clearContents()
              NSPasteboard.general.setString(
                AgentPrompts.auditBuild(buildIdentity),
                forType: .string
              )
              copiedAuditPrompt = true
              Task { @MainActor in
                try? await Task.sleep(for: .seconds(4))
                copiedAuditPrompt = false
              }
            }
            .buttonStyle(.bordered)
          }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(6)
      }
    }
  }
}
