import SwiftUI

struct GmailConnectionNotice: View {
  let onContinue: () -> Void
  let onCancel: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 20) {
      Text("Connect Gmail")
        .font(.title.bold())

      VStack(alignment: .leading, spacing: 14) {
        Text(
          "OpenTrawl downloads your emails from Gmail. It downloads new emails each time "
            + "you sync. Your emails stay on this Mac."
        )

        Text(
          "After you sign in, Google gives OpenTrawl a private credential (a refresh token) "
            + "so it can download your emails. OpenTrawl stores the credential on this Mac "
            + "using the Secure Enclave. Only the signed OpenTrawl app on this Mac can read "
            + "it; other apps on this machine (or a rogue AI) cannot. The credential cannot "
            + "be used on another Mac either."
        )

        Text(
          "The OpenTrawl app on this Mac downloads your emails directly from Google, not "
            + "through any OpenTrawl servers. We don’t even have any servers. We do not "
            + "receive your email or your Google account details. We don’t even know "
            + "if you or any other user connected OpenTrawl to Gmail (unless you decide to "
            + "send us a tweet!)"
        )
      }
      .foregroundStyle(.secondary)
      .fixedSize(horizontal: false, vertical: true)

      Link(
        "Read the privacy policy",
        destination: URL(string: "https://opentrawl.sh/privacy")!
      )

      Divider()

      HStack {
        Spacer()
        Button("Cancel", action: onCancel)
          .keyboardShortcut(.cancelAction)
        Button("Continue to Google", action: onContinue)
          .buttonStyle(.borderedProminent)
          .keyboardShortcut(.defaultAction)
      }
      .controlSize(.large)
    }
    .padding(28)
    .frame(width: 560)
  }
}
