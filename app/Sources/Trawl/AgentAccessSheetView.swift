import AppKit
import SwiftUI

struct SemanticSearchPopoverView: View {
  @Bindable var helperController: TrawlHelperController

  var body: some View {
    VStack(alignment: .leading, spacing: 14) {
      HStack(alignment: .center, spacing: 10) {
        Image(systemName: "brain.head.profile")
          .font(.title3)
          .foregroundStyle(helperController.semanticStatus.isEnabled ? Color.green : .secondary)
        VStack(alignment: .leading, spacing: 2) {
          Text(OperationalCopy.Home.semanticSearchTitle)
            .font(.headline)
          Text(
            helperController.semanticStatus.isEnabled
              ? "Hybrid keyword and vector search is active."
              : "Off. Keyword search runs locally without embeddings."
          )
          .font(.caption)
          .foregroundStyle(.secondary)
        }
        Spacer(minLength: 8)
        if helperController.isUpdatingSemanticSearch {
          ProgressView()
            .controlSize(.small)
        } else {
          Toggle("", isOn: $helperController.semanticSearchEnabledToggle)
            .labelsHidden()
            .toggleStyle(.switch)
            .disabled(!helperController.semanticStatus.isAvailable)
        }
      }

      if helperController.semanticStatus.isAvailable {
        RecordMetadataCard {
          if let chunks = helperController.semanticStatus.chunkSummary {
            LabeledContent("Indexed", value: chunks)
          }
          if let size = helperController.semanticStatus.sizeSummary {
            LabeledContent("Index size", value: size)
          }
          if let model = helperController.semanticStatus.modelSummary {
            LabeledContent("Local model", value: model)
          }
          if let helper = helperController.semanticStatus.helperSummary {
            LabeledContent("Helper", value: helper)
          }
        }
        .font(.caption)
      }

      if let errorMessage = helperController.semanticErrorMessage {
        Text(errorMessage)
          .font(.caption)
          .foregroundStyle(.red)
      }

      Text("Embeddings are disposable and run locally on your Mac. Nothing is sent to a server.")
        .font(.caption2)
        .foregroundStyle(.tertiary)
    }
    .padding(16)
    .frame(width: 340)
    .task {
      await helperController.refreshSemanticStatus()
    }
  }
}

struct AgentAccessSheetView: View {
  let helperController: TrawlHelperController
  let aiInstruction: String
  let terminalCommand: String
  let onDismiss: () -> Void

  @State private var copiedItem: String?
  @State private var copyResetTask: Task<Void, Never>?

  var body: some View {
    VStack(spacing: 0) {
      HStack(alignment: .top, spacing: 12) {
        VStack(alignment: .leading, spacing: 4) {
          Text(OperationalCopy.AgentAccess.sheetTitle)
            .font(.title3.weight(.semibold))
          Text(OperationalCopy.AgentAccess.sheetSubtitle)
            .font(.callout)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
        }
        Spacer(minLength: 12)
        Button(OperationalCopy.SharedAction.done, action: onDismiss)
          .buttonStyle(.borderedProminent)
          .controlSize(.small)
          .tint(TrawlDesign.brandRed)
          .keyboardShortcut(.cancelAction)
      }
      .padding(20)

      Divider()

      ScrollView {
        VStack(alignment: .leading, spacing: 18) {
          QuickConnectActionsSection(
            copiedItem: copiedItem,
            onCopyPrompt: {
              copyToPasteboard(aiInstruction, tag: "prompt")
            },
            onCopyConnectCommand: {
              copyToPasteboard("trawl connect --write", tag: "connect")
            },
            onCopyTerminalCommand: {
              copyToPasteboard(terminalCommand, tag: "terminal")
            }
          )

          ScopedTokensSection(
            tokens: helperController.scopedTokens,
            isLoading: helperController.isLoadingAgentAccess,
            errorMessage: helperController.agentAccessErrorMessage,
            onRevoke: { tokenID in
              Task { await helperController.revokeToken(tokenID) }
            }
          )

          if !helperController.detectedAgents.isEmpty {
            DetectedAgentsSection(agents: helperController.detectedAgents)
          }
        }
        .padding(20)
      }
    }
    .frame(width: 580, height: 520)
    .background(TrawlDesign.canvasBackground)
    .task {
      await helperController.refreshAgentAccess()
    }
  }

  private func copyToPasteboard(_ text: String, tag: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(text, forType: .string)
    copiedItem = tag
    copyResetTask?.cancel()
    copyResetTask = Task { @MainActor in
      try? await Task.sleep(for: .seconds(3))
      guard !Task.isCancelled else { return }
      if copiedItem == tag { copiedItem = nil }
    }
  }
}

private struct QuickConnectActionsSection: View {
  let copiedItem: String?
  let onCopyPrompt: () -> Void
  let onCopyConnectCommand: () -> Void
  let onCopyTerminalCommand: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 10) {
      Text(OperationalCopy.AgentAccess.quickPromptHeading)
        .font(.caption.weight(.semibold))
        .foregroundStyle(.secondary)
      HStack(spacing: 10) {
        Button(action: onCopyPrompt) {
          Label(
            copiedItem == "prompt"
              ? OperationalCopy.Home.copiedAIPromptConfirmation
              : HumanCopy.ConnectAI.copyAction,
            systemImage: copiedItem == "prompt" ? "checkmark" : "sparkles"
          )
        }
        .buttonStyle(.borderedProminent)
        .tint(TrawlDesign.brandRed)

        Button(action: onCopyConnectCommand) {
          Label(
            copiedItem == "connect" ? OperationalCopy.SharedAction.copied : "Copy trawl connect",
            systemImage: copiedItem == "connect" ? "checkmark" : "link.badge.plus"
          )
        }
        .buttonStyle(.bordered)

        Button(action: onCopyTerminalCommand) {
          Label(
            copiedItem == "terminal" ? OperationalCopy.SharedAction.copied : "Copy Terminal path",
            systemImage: copiedItem == "terminal" ? "checkmark" : "apple.terminal"
          )
        }
        .buttonStyle(.bordered)
      }
      .controlSize(.regular)
    }
  }
}

private struct ScopedTokensSection: View {
  let tokens: [ScopedAgentTokenRow]
  let isLoading: Bool
  let errorMessage: String?
  let onRevoke: (String) -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      HStack {
        Text(OperationalCopy.AgentAccess.tokensHeading)
          .font(.caption.weight(.semibold))
          .foregroundStyle(.secondary)
        Spacer()
        if isLoading {
          ProgressView()
            .controlSize(.mini)
        }
      }
      if let errorMessage {
        Text(errorMessage)
          .font(.caption)
          .foregroundStyle(.red)
      }
      RecordMetadataCard {
        if tokens.isEmpty {
          Text(LocalizedStringKey(OperationalCopy.AgentAccess.noTokensYet))
            .font(.callout)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
        } else {
          ForEach(Array(tokens.enumerated()), id: \.element.id) { index, token in
            HStack(alignment: .center, spacing: 10) {
              VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                  Text(token.agentName)
                    .font(.callout.weight(.semibold))
                  Text(token.tokenID)
                    .font(.caption2.monospaced())
                    .foregroundStyle(.tertiary)
                }
                Text("Apps: \(token.allowedApps)")
                  .font(.caption)
                  .foregroundStyle(.secondary)
              }
              Spacer()
              if token.isRevoked {
                Text(OperationalCopy.AgentAccess.revokedLabel)
                  .font(.caption.weight(.medium))
                  .foregroundStyle(.tertiary)
              } else {
                Button(OperationalCopy.AgentAccess.revokeAction) {
                  onRevoke(token.tokenID)
                }
                .buttonStyle(.bordered)
                .controlSize(.mini)
              }
            }
            if index < tokens.count - 1 {
              Divider()
            }
          }
        }
      }
    }
  }
}

private struct DetectedAgentsSection: View {
  let agents: [DetectedLocalAIAgentRow]

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text(OperationalCopy.AgentAccess.detectedAgentsHeading)
        .font(.caption.weight(.semibold))
        .foregroundStyle(.secondary)
      RecordMetadataCard {
        ForEach(Array(agents.enumerated()), id: \.element.id) { index, agent in
          HStack(alignment: .center, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
              Text(agent.displayName)
                .font(.callout.weight(.medium))
              if let path = agent.configPath {
                Text(path)
                  .font(.caption2.monospaced())
                  .foregroundStyle(.tertiary)
                  .lineLimit(1)
              }
            }
            Spacer()
            Text(agent.statusText)
              .font(.caption)
              .foregroundStyle(.secondary)
          }
          if index < agents.count - 1 {
            Divider()
          }
        }
      }
    }
  }
}
