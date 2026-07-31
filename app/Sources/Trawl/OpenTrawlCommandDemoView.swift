import AppKit
import SwiftUI
import TrawlClient

struct OpenTrawlCommandDemoView: View {
  private static let outputPresentation: OpenTrawlCommandOutputPresentation = .animated

  @State private var playback: OpenTrawlCommandDemoPlayback

  let onBack: () -> Void
  let onFinish: () -> Void

  init(
    helperURL: URL,
    onBack: @escaping () -> Void,
    onFinish: @escaping () -> Void
  ) {
    let helperDirectoryPath = helperURL.deletingLastPathComponent().path
    let outputFont = NSFont.monospacedSystemFont(
      ofSize: TrawlDesign.commandDemoOutputFontSize,
      weight: .regular
    )
    let outputWidth =
      TrawlDesign.commandDemoPageWidth - TrawlDesign.commandDemoTerminalContentInset * 2
    let outputColumnCount = Int(outputWidth / outputFont.maximumAdvancement.width)
    _playback = State(
      initialValue: OpenTrawlCommandDemoPlayback(
        commandRunner: PackagedOpenTrawlCommandRunner(
          helperURL: helperURL,
          outputColumnCount: outputColumnCount
        ),
        journey: OpenTrawlCommandDemoJourney(
          client: ProcessTrawlClient(binaryURL: helperURL)
        ),
        helperDirectoryPath: helperDirectoryPath,
        outputPresentation: Self.outputPresentation
      )
    )
    self.onBack = onBack
    self.onFinish = onFinish
  }

  init(
    playback: OpenTrawlCommandDemoPlayback,
    onBack: @escaping () -> Void,
    onFinish: @escaping () -> Void
  ) {
    _playback = State(initialValue: playback)
    self.onBack = onBack
    self.onFinish = onFinish
  }

  var body: some View {
    TrawlFlowScaffold(
      page: .commandDemo,
      contentWidth: TrawlDesign.commandDemoPageWidth,
      footerWidth: TrawlDesign.commandDemoPageWidth
    ) {
      OpenTrawlCommandDemoTerminal(
        comment: playback.currentStep.comment,
        command: playback.visibleCommand,
        output: playback.visibleOutput,
        phase: playback.phase
      )
    } actions: {
      OpenTrawlCommandDemoActions(
        onBack: {
          Task {
            await playback.stop()
            onBack()
          }
        },
        onFinish: {
          Task {
            await playback.stop()
            onFinish()
          }
        }
      )
    }
    .task {
      playback.start()
    }
    .onDisappear {
      Task { await playback.stop() }
    }
  }
}

private struct OpenTrawlCommandDemoTerminal: View {
  let comment: String
  let command: String
  let output: String
  let phase: OpenTrawlCommandDemoPlaybackPhase

  var body: some View {
    VStack(spacing: 0) {
      OpenTrawlCommandDemoTerminalHeader(isRunning: phase == .runningCommand)
      Divider()
        .overlay(Color.white.opacity(0.08))
      VStack(alignment: .leading, spacing: 14) {
        OpenTrawlCommandDemoComment(comment: comment)
        OpenTrawlCommandDemoTypedCommand(command: command)
        Divider()
          .overlay(Color.white.opacity(0.08))
        OpenTrawlCommandDemoOutputViewport(
          output: output,
          followsOutput: phase == .revealingOutput
        )
      }
      .padding(TrawlDesign.commandDemoTerminalContentInset)
      .opacity(phase == .transitioning ? 0 : 1)
    }
    .frame(maxWidth: .infinity)
    .frame(height: TrawlDesign.commandDemoTerminalHeight)
    .background(Color(red: 0.045, green: 0.055, blue: 0.075))
    .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
    .overlay {
      RoundedRectangle(cornerRadius: 16, style: .continuous)
        .strokeBorder(Color.white.opacity(0.09))
    }
    .accessibilityElement(children: .contain)
    .accessibilityLabel(DraftCopy.CommandDemo.terminalTitle)
  }
}

private struct OpenTrawlCommandDemoTerminalHeader: View {
  let isRunning: Bool

  var body: some View {
    HStack(spacing: 9) {
      Circle().fill(Color.red.opacity(0.9)).frame(width: 10, height: 10)
      Circle().fill(Color.yellow.opacity(0.9)).frame(width: 10, height: 10)
      Circle().fill(Color.green.opacity(0.9)).frame(width: 10, height: 10)
      Spacer()
      Text(DraftCopy.CommandDemo.terminalTitle)
        .font(.system(.caption, design: .monospaced))
        .foregroundStyle(.white.opacity(0.54))
      Spacer()
      ProgressView()
        .controlSize(.small)
        .tint(.white.opacity(0.7))
        .opacity(isRunning ? 1 : 0)
        .frame(width: 28)
        .accessibilityHidden(true)
    }
    .padding(.horizontal, 14)
    .frame(height: 42)
  }

}

private struct OpenTrawlCommandDemoComment: View {
  let comment: String

  var body: some View {
    Text(verbatim: "# \(comment)")
      .font(.system(size: 16, weight: .regular, design: .monospaced))
      .foregroundStyle(Color(red: 0.49, green: 0.76, blue: 0.58))
      .lineLimit(1)
      .frame(maxWidth: .infinity, minHeight: 18, alignment: .leading)
  }
}

private struct OpenTrawlCommandDemoTypedCommand: View {
  let command: String

  var body: some View {
    Text(verbatim: "❯ \(command)")
      .font(.system(size: 18, weight: .medium, design: .monospaced))
      .foregroundStyle(.white)
      .lineLimit(1)
      .frame(maxWidth: .infinity, minHeight: 18, alignment: .leading)
  }
}

private struct OpenTrawlCommandDemoOutputViewport: NSViewRepresentable {
  let output: String
  let followsOutput: Bool

  func makeNSView(context _: Context) -> NSScrollView {
    let scrollView = NSScrollView()
    scrollView.drawsBackground = false
    scrollView.hasVerticalScroller = true
    scrollView.hasHorizontalScroller = true
    scrollView.autohidesScrollers = true

    let textView = NSTextView()
    textView.isEditable = false
    textView.isSelectable = true
    textView.drawsBackground = false
    textView.textColor = NSColor.white.withAlphaComponent(0.68)
    textView.font = NSFont.monospacedSystemFont(
      ofSize: TrawlDesign.commandDemoOutputFontSize,
      weight: .regular
    )
    textView.textContainerInset = .zero
    textView.isHorizontallyResizable = true
    textView.isVerticallyResizable = true
    textView.maxSize = NSSize(
      width: CGFloat.greatestFiniteMagnitude,
      height: CGFloat.greatestFiniteMagnitude
    )
    textView.textContainer?.containerSize = NSSize(
      width: CGFloat.greatestFiniteMagnitude,
      height: CGFloat.greatestFiniteMagnitude
    )
    textView.textContainer?.lineFragmentPadding = 0
    textView.textContainer?.widthTracksTextView = false
    scrollView.documentView = textView
    return scrollView
  }

  func updateNSView(_ scrollView: NSScrollView, context _: Context) {
    guard let textView = scrollView.documentView as? NSTextView else { return }
    guard textView.string != output else { return }
    textView.string = output
    if followsOutput {
      textView.scrollToEndOfDocument(nil)
    }
  }
}

private struct OpenTrawlCommandDemoActions: View {
  let onBack: () -> Void
  let onFinish: () -> Void

  var body: some View {
    HStack(spacing: 14) {
      Button(OperationalCopy.SharedAction.back, action: onBack)
        .buttonStyle(.plain)
        .foregroundStyle(.secondary)
      Spacer()
      Button(DraftCopy.CommandDemo.finishAction, action: onFinish)
        .buttonStyle(.borderedProminent)
    }
  }
}
