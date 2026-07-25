import Foundation

#warning("OpenTrawl is using AI-authored DraftCopy. Josh has not approved this copy for release.")

// AI-AUTHORED PRODUCT COPY. THIS TEXT IS A DRAFT.
// Agents may edit this file. Agents must never move any part of it into
// HumanCopy.swift. Only Josh can write or approve the contents of HumanCopy.swift.
enum DraftCopy {
  enum Status {
    static let badge = "DRAFT COPY"
    static let accessibilityLabel = "Draft copy. Josh has not approved this text."
  }

  enum Welcome {
    static let title = "Meet OpenTrawl."
    static let body =
      "It turns the apps on this Mac into a private, searchable archive for you and your AI."
    static let privacy =
      "Your archive stays on this Mac. OpenTrawl reads your apps but never changes them."
    static let primaryAction = "Continue"
  }

  enum FullDiskAccess {
    static let title = "OpenTrawl needs Full Disk Access."
    static let body =
      "macOS keeps Messages, Notes, Contacts and Calendar data in protected folders. OpenTrawl needs permission to read it and build your archive."
    static let purpose =
      "Your archive stays on this Mac. OpenTrawl does not change your apps."
    static let trustGroupTitle = "Verify OpenTrawl"
    static let trustGroupBody =
      "Read the public source code, or copy a prompt that asks your coding AI to audit this build."
    static let readCodeAction = "Read the code on GitHub"
    static let dragAccessibilityLabel = "Drag OpenTrawl to Full Disk Access"
  }

  enum ArchiveBuild {
    static let title = "Your archive is taking shape."
    static let body =
      "Each app becomes searchable as soon as it is ready. You can connect your AI while OpenTrawl keeps working."
    static let readyTitle = "Your archive is ready."
    static let readyBody =
      "Your apps are searchable. You can connect your AI now or do it later."
  }

  enum ConnectAI {
    static let title = "Connect your AI"
    static let body =
      "Copy instructions into your coding AI. OpenTrawl does not install anything or change its settings."
  }
}
