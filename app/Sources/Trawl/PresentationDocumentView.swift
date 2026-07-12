import SwiftUI
import TrawlClient

struct PresentationDocumentView: View {
  let sourceID: String
  let openRef: String
  let document: PresentationDocument
  var body: some View {
    ScrollView {
      VStack(alignment: .leading, spacing: 16) {
        Text(document.title).font(.title2).textSelection(.enabled).accessibilityLabel("\(sourceID), \(document.title), \(openRef)")
        ForEach(Array(document.blocks.enumerated()), id: \.offset) { _, block in BlockView(block: block) }
        ForEach(Array(document.actions.enumerated()), id: \.offset) { _, action in ActionView(action: action) }
        ForEach(Array(document.facts.enumerated()), id: \.offset) { _, fact in FactView(fact: fact) }
      }.padding(18).frame(maxWidth: .infinity, alignment: .leading)
    }
  }
}

private struct BlockView: View {
  let block: PresentationBlock
  var body: some View {
    switch block {
    case let .heading(text): Text(text).font(.headline).textSelection(.enabled)
    case let .prose(text): Text(text).textSelection(.enabled)
    case let .fields(fields): Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 6) { ForEach(Array(fields.enumerated()), id: \.offset) { _, field in GridRow { Text(field.label).foregroundStyle(.secondary); Text(field.display).textSelection(.enabled) } } }
    case let .table(columns, rows): ScrollView(.horizontal) { Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 7) { GridRow { ForEach(Array(columns.enumerated()), id: \.offset) { _, column in Text(column).font(.caption.weight(.semibold)) } }; ForEach(Array(rows.enumerated()), id: \.offset) { _, row in GridRow { ForEach(Array(row.cells.enumerated()), id: \.offset) { _, cell in Text(cell).textSelection(.enabled) } }.padding(4).background(row.role == .target ? Color.accentColor.opacity(0.12) : .clear).accessibilityLabel(row.role == .target ? "Target row" : "Row") } } }
    case let .resource(resource): VStack(alignment: .leading, spacing: 4) { Text(resource.label).font(.headline); Text("\(resourceKind(resource.kind)): \(resource.ref)").font(.callout).foregroundStyle(.secondary).textSelection(.enabled); ForEach(Array(resource.metadata.enumerated()), id: \.offset) { _, field in Text("\(field.label): \(field.display)").font(.caption) } }
    }
  }
  private func resourceKind(_ kind: PresentationResourceKind) -> String { switch kind { case .file: "File"; case .image: "Image"; case .video: "Video"; case .audio: "Audio" } }
}
private struct ActionView: View { let action: PresentationAction; var body: some View { switch action.target { case let .url(url): Link(action.label, destination: url).accessibilityLabel("Action: \(action.label)"); case let .openRef(ref): VStack(alignment: .leading) { Text(action.label); Text(ref).font(.caption).textSelection(.enabled) }.disabled(true).accessibilityLabel("Open reference action: \(action.label), \(ref), unavailable") } } }
private struct FactView: View { let fact: PresentationFact; var body: some View { VStack(alignment: .leading, spacing: 2) { Text(label).font(.callout.weight(.semibold)); Text(fact.message); if !fact.remedy.isEmpty { Text(fact.remedy).foregroundStyle(.secondary) } }.font(.callout).accessibilityLabel("\(label): \(fact.message)\(fact.remedy.isEmpty ? "" : ". \(fact.remedy)")") }; private var label: String { switch fact.kind { case .truncation: "Truncated"; case .provenance: "Provenance"; case .warning: "Warning"; case .error: "Error" } } }
