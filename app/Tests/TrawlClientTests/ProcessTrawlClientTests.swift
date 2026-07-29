import Foundation
import Testing

@testable import TrawlClient

@Test func runtimeConfigurationKeepsProductionDefaultsAndQuotesAnIsolatedAlpha() {
  let production = TrawlRuntimeConfiguration(
    bundleURL: URL(fileURLWithPath: "/Applications/OpenTrawl.app"),
    environment: [:]
  )
  #expect(production.stateRoot == nil)
  #expect(production.helperURL.path == "/Applications/OpenTrawl.app/Contents/Helpers/trawl")
  #expect(production.agentCommand == "/Applications/OpenTrawl.app/Contents/Helpers/trawl")

  let alpha = TrawlRuntimeConfiguration(
    bundleURL: URL(fileURLWithPath: "/Applications/OpenTrawl Alpha.app"),
    environment: [TrawlRuntimeConfiguration.stateRootEnvironmentKey: "/tmp/OpenTrawl Alpha"]
  )
  #expect(alpha.stateRoot == "/tmp/OpenTrawl Alpha")
  #expect(
    alpha.agentCommand
      == "env OPENTRAWL_STATE_ROOT='/tmp/OpenTrawl Alpha' '/Applications/OpenTrawl Alpha.app/Contents/Helpers/trawl'"
  )
}
