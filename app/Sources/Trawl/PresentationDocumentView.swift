import SwiftUI
import TrawlClient

struct PresentationDocumentView: View {
  let document: PresentationDocument

  var body: some View {
    ScrollView {
      VStack(alignment: .leading, spacing: 16) {
        Text(document.title)
          .font(.title2)
          .textSelection(.enabled)
          .accessibilityLabel(document.title)
        ForEach(Array(document.blocks.enumerated()), id: \.offset) { _, block in
          BlockView(block: block)
        }
        ForEach(Array(document.actions.enumerated()), id: \.offset) { _, action in
          ActionView(action: action)
        }
        ForEach(Array(document.facts.enumerated()), id: \.offset) { _, fact in
          FactView(fact: fact)
        }
      }
      .padding(18)
      .frame(maxWidth: .infinity, alignment: .leading)
    }
  }
}

private struct BlockView: View {
  let block: PresentationBlock

  var body: some View {
    switch block {
    case let .heading(text):
      Text(text)
        .font(.headline)
        .textSelection(.enabled)
    case let .prose(text):
      Text(text)
        .textSelection(.enabled)
    case let .fields(fields):
      VStack(alignment: .leading, spacing: 6) {
        ForEach(Array(fields.enumerated()), id: \.offset) { _, field in
          HStack(alignment: .firstTextBaseline, spacing: 16) {
            Text(field.label)
              .foregroundStyle(.secondary)
              .frame(width: 112, alignment: .leading)
            Text(field.display)
              .textSelection(.enabled)
            Spacer(minLength: 0)
          }
          .accessibilityElement(children: .ignore)
          .accessibilityLabel("\(field.label): \(field.display)")
        }
      }
    case let .table(columns, rows):
      ResponsiveTable(columns: columns, rows: rows)
    case let .resource(resource):
      ResourceView(resource: resource)
    }
  }
}

private struct ResponsiveTable: View {
  let columns: [String]
  let rows: [PresentationRow]

  var body: some View {
    ViewThatFits(in: .horizontal) {
      Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 7) {
        GridRow {
          ForEach(Array(columns.enumerated()), id: \.offset) { _, column in
            Text(column)
              .font(.caption.weight(.semibold))
              .frame(minWidth: 120, alignment: .leading)
          }
        }
        ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
          GridRow {
            ForEach(Array(row.cells.enumerated()), id: \.offset) { _, cell in
              Text(cell)
                .textSelection(.enabled)
                .frame(minWidth: 120, alignment: .leading)
            }
          }
          .padding(6)
          .background(row.role == .target ? Color.accentColor.opacity(0.12) : .clear)
          .accessibilityElement(children: .ignore)
          .accessibilityLabel(accessibilityLabel(for: row))
        }
      }
      .fixedSize(horizontal: true, vertical: false)
      VStack(alignment: .leading, spacing: 8) {
        ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
          VStack(alignment: .leading, spacing: 5) {
            ForEach(columns.indices, id: \.self) { index in
              LabeledContent(columns[index], value: value(at: index, in: row))
                .font(.callout)
            }
          }
          .padding(10)
          .frame(maxWidth: .infinity, alignment: .leading)
          .background(row.role == .target ? Color.accentColor.opacity(0.12) : .secondary.opacity(0.05), in: .rect(cornerRadius: 8))
          .accessibilityElement(children: .ignore)
          .accessibilityLabel(accessibilityLabel(for: row))
        }
      }
    }
  }

  private func value(at index: Int, in row: PresentationRow) -> String {
    index < row.cells.count ? row.cells[index] : ""
  }

  private func accessibilityLabel(for row: PresentationRow) -> String {
    let fields = columns.indices.map { index in
      "\(columns[index]): \(value(at: index, in: row))"
    }
    let prefix = row.role == .target ? "Target. " : ""
    return prefix + fields.joined(separator: ". ")
  }
}

private struct LabeledContent: View {
  let label: String
  let value: String

  init(_ label: String, value: String) {
    self.label = label
    self.value = value
  }

  var body: some View {
    HStack(alignment: .firstTextBaseline, spacing: 10) {
      Text(label)
        .foregroundStyle(.secondary)
      Text(value)
        .textSelection(.enabled)
      Spacer(minLength: 0)
    }
  }
}

private struct ResourceView: View {
  let resource: PresentationResource

  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
      Label(resource.label, systemImage: symbol)
        .font(.headline)
      Text(kindLabel)
        .font(.callout)
        .foregroundStyle(.secondary)
      ForEach(Array(resource.metadata.enumerated()), id: \.offset) { _, field in
        LabeledContent(field.label, value: field.display)
          .font(.caption)
      }
    }
    .padding(10)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(.secondary.opacity(0.05), in: .rect(cornerRadius: 8))
  }

  private var symbol: String {
    switch resource.kind {
    case .file: "doc"
    case .image: "photo"
    case .video: "video"
    case .audio: "waveform"
    }
  }

  private var kindLabel: String {
    switch resource.kind {
    case .file: "File"
    case .image: "Image"
    case .video: "Video"
    case .audio: "Audio"
    }
  }
}

private struct ActionView: View {
  let action: PresentationAction

  var body: some View {
    switch action.target {
    case let .url(url):
      Link(action.label, destination: url)
        .accessibilityLabel(action.label)
    case .openRef:
      VStack(alignment: .leading, spacing: 2) {
        Text(action.label)
        Text("Unavailable in this preview")
          .font(.caption)
          .foregroundStyle(.secondary)
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
      Text(label)
        .font(.callout.weight(.semibold))
      Text(fact.message)
      if !fact.remedy.isEmpty {
        Text(fact.remedy)
          .foregroundStyle(.secondary)
      }
    }
    .font(.callout)
    .accessibilityElement(children: .ignore)
    .accessibilityLabel(
      "\(label): \(fact.message)\(fact.remedy.isEmpty ? "" : ". \(fact.remedy)")"
    )
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
