import SwiftUI
import TrawlClient

enum PresentationElementID: Hashable {
  case anchor(String)
  case block(Int)
  case field(block: Int, field: Int)
  case row(block: Int, row: Int)
  case resource(Int)
  case resourceField(block: Int, field: Int)

  static func sourceAnchor(_ anchorID: String, fallback: Self) -> Self {
    anchorID.isEmpty ? fallback : .anchor(anchorID)
  }
}

struct PresentationDocumentView: View {
  let client: any TrawlClient
  let sourceID: String
  let document: PresentationDocument
  let targetAnchorID: String

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          Text(document.title)
            .font(.title2)
            .textSelection(.enabled)
            .accessibilityLabel(document.title)
          ForEach(Array(document.blocks.enumerated()), id: \.offset) { index, block in
            BlockView(client: client, sourceID: sourceID, block: block, index: index)
          }
          ForEach(Array(document.actions.enumerated()), id: \.offset) { _, action in
            ActionView(action: action)
          }
          ForEach(Array(document.facts.enumerated()), id: \.offset) { _, fact in
            FactView(fact: fact)
          }
        }
        .padding(18)
        .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
        .frame(maxWidth: .infinity, alignment: .leading)
      }
      .onAppear {
        proxy.scrollTo(PresentationElementID.anchor(targetAnchorID), anchor: .center)
      }
    }
  }
}

private struct BlockView: View {
  let client: any TrawlClient
  let sourceID: String
  let block: PresentationBlock
  let index: Int

  var body: some View {
    switch block {
    case .heading(let anchorID, let text):
      Text(text).font(.headline).textSelection(.enabled)
        .id(PresentationElementID.sourceAnchor(anchorID, fallback: .block(index)))
    case .prose(let anchorID, let text):
      Text(text).textSelection(.enabled)
        .id(PresentationElementID.sourceAnchor(anchorID, fallback: .block(index)))
    case .fields(let anchorID, let fields):
      VStack(alignment: .leading, spacing: 6) {
        ForEach(Array(fields.enumerated()), id: \.offset) { fieldIndex, field in
          HStack(alignment: .firstTextBaseline, spacing: 16) {
            Text(field.label).foregroundStyle(.secondary).frame(width: 112, alignment: .leading)
            Text(field.display).textSelection(.enabled)
            Spacer(minLength: 0)
          }
          .accessibilityElement(children: .ignore)
          .accessibilityLabel("\(field.label): \(field.display)")
          .id(
            PresentationElementID.sourceAnchor(
              field.anchorID, fallback: .field(block: index, field: fieldIndex)))
        }
      }
      .id(PresentationElementID.sourceAnchor(anchorID, fallback: .block(index)))
    case .table(let anchorID, let columns, let rows):
      ResponsiveTable(columns: columns, rows: rows, blockIndex: index)
        .id(PresentationElementID.sourceAnchor(anchorID, fallback: .block(index)))
    case .resource(let anchorID, let resource):
      PresentationResourceView(
        client: client, sourceID: sourceID, resource: resource, blockIndex: index
      )
      .id(PresentationElementID.sourceAnchor(anchorID, fallback: .block(index)))
    }
  }
}

private struct ResponsiveTable: View {
  let columns: [String]
  let rows: [PresentationRow]
  let blockIndex: Int

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      ForEach(Array(rows.enumerated()), id: \.offset) { rowIndex, row in
        ViewThatFits(in: .horizontal) {
          HStack(alignment: .firstTextBaseline, spacing: 14) {
            ForEach(columns.indices, id: \.self) { index in
              VStack(alignment: .leading, spacing: 3) {
                Text(columns[index]).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                Text(value(at: index, in: row)).textSelection(.enabled)
              }
              .frame(minWidth: 120, alignment: .leading)
            }
          }
          VStack(alignment: .leading, spacing: 5) {
            ForEach(columns.indices, id: \.self) { index in
              LabeledContent(columns[index], value: value(at: index, in: row)).font(.callout)
            }
          }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
          row.role == .target ? Color.accentColor.opacity(0.12) : .secondary.opacity(0.05),
          in: .rect(cornerRadius: 8)
        )
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityLabel(for: row))
        .id(
          PresentationElementID.sourceAnchor(
            row.anchorID, fallback: .row(block: blockIndex, row: rowIndex)))
      }
    }
  }

  private func value(at index: Int, in row: PresentationRow) -> String {
    index < row.cells.count ? row.cells[index] : ""
  }

  private func accessibilityLabel(for row: PresentationRow) -> String {
    let fields = columns.indices.map { "\(columns[$0]): \(value(at: $0, in: row))" }
    return (row.role == .target ? "Target. " : "") + fields.joined(separator: ". ")
  }
}

struct LabeledContent: View {
  let label: String
  let value: String
  init(_ label: String, value: String) {
    self.label = label
    self.value = value
  }
  var body: some View {
    HStack(alignment: .firstTextBaseline, spacing: 10) {
      Text(label).foregroundStyle(.secondary)
      Text(value).textSelection(.enabled)
      Spacer(minLength: 0)
    }
  }
}

private struct ActionView: View {
  let action: PresentationAction
  var body: some View {
    switch action.target {
    case .url(let url): Link(action.label, destination: url).accessibilityLabel(action.label)
    case .openRef:
      VStack(alignment: .leading, spacing: 2) {
        Text(action.label)
        Text("Unavailable in this preview").font(.caption).foregroundStyle(.secondary)
      }
      .disabled(true)
      .accessibilityElement(children: .ignore)
      .accessibilityLabel(action.label)
      .accessibilityValue("Unavailable in this preview")
    }
  }
}

private struct FactView: View {
  let fact: PresentationFact
  var body: some View {
    VStack(alignment: .leading, spacing: 2) {
      Text(label).font(.callout.weight(.semibold))
      Text(fact.message)
      if !fact.remedy.isEmpty { Text(fact.remedy).foregroundStyle(.secondary) }
    }
    .font(.callout)
    .accessibilityElement(children: .ignore)
    .accessibilityLabel("\(label): \(fact.message)\(fact.remedy.isEmpty ? "" : ". \(fact.remedy)")")
  }
  private var label: String {
    switch fact.kind {
    case .truncation: "Truncated"
    case .provenance: "Provenance"
    case .warning: "Warning"
    case .error: "Error"
    }
  }
}
