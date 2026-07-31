import Foundation
import Observation

enum OpenTrawlCommandOutputPresentation: Sendable, Equatable {
  case animated
  case immediate
}

enum OpenTrawlCommandDemoPlaybackPhase: Sendable, Equatable {
  case typingCommand
  case runningCommand
  case revealingOutput
  case dwelling
  case transitioning
  case finished
}

struct OpenTrawlCommandDemoTiming: Sendable, Equatable {
  let commandCharacterDelay: Duration
  let outputChunkDelay: Duration
  let commandTransition: Duration

  static let restrained = OpenTrawlCommandDemoTiming(
    commandCharacterDelay: .milliseconds(22),
    outputChunkDelay: .milliseconds(18),
    commandTransition: .milliseconds(220)
  )
}

@MainActor
@Observable
final class OpenTrawlCommandDemoPlayback {
  private(set) var currentStepIndex = 0
  private(set) var visibleCommand = ""
  private(set) var visibleOutput = ""
  private(set) var phase: OpenTrawlCommandDemoPlaybackPhase = .typingCommand

  let steps: [OpenTrawlCommandDemoStep]

  private let commandRunner: any OpenTrawlCommandRunning
  private let journey: OpenTrawlCommandDemoJourney
  private let helperDirectoryPath: String
  private let outputPresentation: OpenTrawlCommandOutputPresentation
  private let timing: OpenTrawlCommandDemoTiming
  private var playbackTask: Task<Void, Never>?

  init(
    steps: [OpenTrawlCommandDemoStep] = OpenTrawlCommandDemoScript.steps,
    commandRunner: any OpenTrawlCommandRunning,
    journey: OpenTrawlCommandDemoJourney,
    helperDirectoryPath: String,
    outputPresentation: OpenTrawlCommandOutputPresentation,
    timing: OpenTrawlCommandDemoTiming = .restrained
  ) {
    precondition(!steps.isEmpty)
    self.steps = steps
    self.commandRunner = commandRunner
    self.journey = journey
    self.helperDirectoryPath = helperDirectoryPath
    self.outputPresentation = outputPresentation
    self.timing = timing
  }

  var currentStep: OpenTrawlCommandDemoStep {
    steps[currentStepIndex]
  }

  private var canAdvance: Bool {
    currentStepIndex < steps.index(before: steps.endIndex)
  }

  func start() {
    guard playbackTask == nil, phase != .finished else { return }
    playbackTask = Task { [weak self] in
      await self?.playFromCurrentStepUntilFinished()
    }
  }

  func stop() async {
    playbackTask?.cancel()
    playbackTask = nil
    await commandRunner.stopRunningTrawl()
  }

  private func playFromCurrentStepUntilFinished() async {
    while !Task.isCancelled {
      guard
        let resolvedArguments = await journey.resolveCommandArguments(
          for: currentStep.instruction
        )
      else {
        phase = .finished
        return
      }
      guard await typeCurrentCommand(arguments: resolvedArguments) else { return }

      switch currentStep.instruction {
      case .changeToPackagedHelperDirectory:
        phase = .dwelling
      default:
        phase = .runningCommand
        let output = await commandRunner.runTrawl(arguments: resolvedArguments)
        guard !Task.isCancelled else { return }
        guard await reveal(output: output) else { return }
        guard output.succeeded else {
          phase = .finished
          return
        }
        phase = .dwelling
      }

      guard await sleepUnlessCancelled(for: currentStep.completedCommandDwell) else {
        return
      }
      guard canAdvance else {
        phase = .finished
        return
      }

      phase = .transitioning
      guard await sleepUnlessCancelled(for: timing.commandTransition) else { return }
      currentStepIndex += 1
      visibleCommand = ""
      visibleOutput = ""
      phase = .typingCommand
    }
  }

  private func typeCurrentCommand(arguments: [String]) async -> Bool {
    phase = .typingCommand
    visibleCommand = ""
    let displayedCommand = currentStep.instruction.displayedCommand(
      helperDirectoryPath: helperDirectoryPath,
      arguments: arguments
    )
    for character in displayedCommand {
      visibleCommand.append(character)
      guard await sleepUnlessCancelled(for: timing.commandCharacterDelay) else {
        return false
      }
    }
    return !Task.isCancelled
  }

  private func reveal(output: OpenTrawlCommandTextOutput) async -> Bool {
    phase = .revealingOutput
    switch outputPresentation {
    case .immediate:
      visibleOutput = output.text
      return !Task.isCancelled
    case .animated:
      visibleOutput = ""
      var remainingOutput = output.text[...]
      while !remainingOutput.isEmpty {
        let chunkEnd =
          remainingOutput.index(
            remainingOutput.startIndex,
            offsetBy: 8,
            limitedBy: remainingOutput.endIndex
          ) ?? remainingOutput.endIndex
        visibleOutput.append(contentsOf: remainingOutput[..<chunkEnd])
        remainingOutput = remainingOutput[chunkEnd...]
        guard await sleepUnlessCancelled(for: timing.outputChunkDelay) else {
          return false
        }
      }
      return !Task.isCancelled
    }
  }

  private func sleepUnlessCancelled(for duration: Duration) async -> Bool {
    do {
      try await Task.sleep(for: duration)
      return !Task.isCancelled
    } catch {
      return false
    }
  }
}
