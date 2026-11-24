import SwiftUI

/// The facts of an opened record as a grid: each `LabeledContent` inside
/// becomes a row with its label in a fixed column and a hairline under it.
struct RecordDetailsGrid<Rows: View>: View {
  @ViewBuilder let rows: Rows

  var body: some View {
    VStack(alignment: .leading, spacing: 0) {
      rows
    }
    .labeledContentStyle(RecordDetailsGridRowStyle())
  }
}

private struct RecordDetailsGridRowStyle: LabeledContentStyle {
  private static let labelColumnWidth: CGFloat = 132

  func makeBody(configuration: Configuration) -> some View {
    VStack(spacing: 0) {
      HStack(alignment: .firstTextBaseline, spacing: TrawlDesign.Space.md) {
        configuration.label
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
          .frame(width: Self.labelColumnWidth, alignment: .leading)
        configuration.content
          .font(TrawlDesign.Typography.body)
          .foregroundStyle(.primary)
          .frame(maxWidth: .infinity, alignment: .leading)
      }
      .padding(.vertical, 7)
      Divider()
    }
  }
}
