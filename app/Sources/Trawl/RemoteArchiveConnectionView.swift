import SwiftUI
import TrawlClient

struct RemoteArchiveConnectionView: View {
  let connectionStore: ArchiveConnectionStore
  let showsLocalOption: Bool
  let onConnectionChanged: () -> Void
  let onDismiss: () -> Void

  @State private var urlText = ""
  @State private var bearerToken = ""
  @State private var isConnecting = false
  @State private var errorMessage: String?

  var body: some View {
    VStack(alignment: .leading, spacing: 14) {
      HStack(alignment: .firstTextBaseline) {
        Text(OperationalCopy.Connection.sheetTitle)
          .font(TrawlDesign.Typography.sectionHeader)
          .foregroundStyle(TrawlDesign.Colors.ink)
        Spacer()
        if connectionStore.isRemote {
          Text(OperationalCopy.Connection.readOnlyBadge)
            .font(TrawlDesign.Typography.monoSmall)
            .foregroundStyle(TrawlDesign.Colors.selectBlue)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(TrawlDesign.Colors.selectBlueSurface, in: Capsule())
        }
      }

      if showsLocalOption && connectionStore.isRemote {
        HStack {
          VStack(alignment: .leading, spacing: 2) {
            Text(OperationalCopy.Connection.connectedToLabel)
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(TrawlDesign.Colors.mutedInk)
            Text(connectionStore.activeRemoteURLString ?? connectionStore.activeHostDisplayName)
              .font(TrawlDesign.Typography.monoSmall)
              .foregroundStyle(TrawlDesign.Colors.ink)
              .lineLimit(1)
              .truncationMode(.middle)
          }
          Spacer()
          Button(OperationalCopy.Connection.switchToThisMac) {
            connectionStore.switchToLocalMac()
            onConnectionChanged()
            onDismiss()
          }
          .buttonStyle(.bordered)
        }
        .padding(10)
        .background(
          TrawlDesign.Colors.surface,
          in: TrawlDesign.Shape.surface
        )
        .overlay {
          TrawlDesign.Shape.surface
            .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
        }
      }

      VStack(alignment: .leading, spacing: 6) {
        Text(OperationalCopy.Connection.serverAddressLabel)
          .font(TrawlDesign.Typography.meta.weight(.medium))
          .foregroundStyle(TrawlDesign.Colors.ink)
        TextField(
          OperationalCopy.Connection.serverAddressPlaceholder,
          text: $urlText
        )
        .textFieldStyle(.roundedBorder)
        .font(TrawlDesign.Typography.monoSmall)
        .disabled(isConnecting)
      }

      VStack(alignment: .leading, spacing: 6) {
        Text(OperationalCopy.Connection.accessTokenLabel)
          .font(TrawlDesign.Typography.meta.weight(.medium))
          .foregroundStyle(TrawlDesign.Colors.ink)
        SecureField(
          connectionStore.hasSavedBearerToken(for: urlText)
            ? OperationalCopy.Connection.savedAccessTokenPlaceholder
            : OperationalCopy.Connection.accessTokenPlaceholder,
          text: $bearerToken
        )
        .textFieldStyle(.roundedBorder)
        .font(TrawlDesign.Typography.monoSmall)
        .disabled(isConnecting)
        .onSubmit(connect)
      }

      if let parsedURL = RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(from: urlText),
        !RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: parsedURL)
      {
        Text(OperationalCopy.Connection.unencryptedHTTPWarning)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.warningAmberText)
          .fixedSize(horizontal: false, vertical: true)
      }

      Text(OperationalCopy.Connection.hostSetupHint)
        .font(TrawlDesign.Typography.meta)
        .foregroundStyle(TrawlDesign.Colors.mutedInk)
        .fixedSize(horizontal: false, vertical: true)

      if let errorMessage {
        Text(errorMessage)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.matchRed)
          .fixedSize(horizontal: false, vertical: true)
      }

      HStack(spacing: 8) {
        if connectionStore.hasSavedBearerToken && !connectionStore.isEnvironmentOverride {
          Button(OperationalCopy.Connection.forgetSavedConnection, role: .destructive) {
            do {
              try connectionStore.forgetSavedRemoteArchive()
              urlText = ""
              bearerToken = ""
              errorMessage = nil
              onConnectionChanged()
            } catch {
              errorMessage = error.localizedDescription
            }
          }
          .buttonStyle(.borderless)
          .disabled(isConnecting)
        }
        Spacer()
        Button(OperationalCopy.Connection.cancel) {
          onDismiss()
        }
        .disabled(isConnecting)
        Button(action: connect) {
          if isConnecting {
            HStack(spacing: 6) {
              ProgressView()
                .controlSize(.small)
              Text(OperationalCopy.Connection.connecting)
            }
          } else {
            Text(OperationalCopy.Connection.connectAction)
          }
        }
        .buttonStyle(.borderedProminent)
        .tint(TrawlDesign.Colors.selectBlue)
        .disabled(isConnecting || urlText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
      }
    }
    .padding(16)
    .frame(width: 400)
    .onAppear {
      if urlText.isEmpty {
        urlText = connectionStore.savedRemoteURLString
      }
    }
  }

  private func connect() {
    guard !isConnecting else { return }
    errorMessage = nil
    isConnecting = true
    let submittedURL = urlText
    let submittedToken = bearerToken
    Task { @MainActor in
      defer { isConnecting = false }
      do {
        try await connectionStore.connectToRemoteArchive(
          urlText: submittedURL,
          bearerToken: submittedToken
        )
        bearerToken = ""
        onConnectionChanged()
        onDismiss()
      } catch {
        errorMessage = error.localizedDescription
      }
    }
  }
}
