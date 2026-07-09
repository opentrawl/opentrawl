package projection

import (
	"errors"
	"fmt"
	"strings"
)

// Field numbers within the table CRDT (MergableDataProto) message tree.
const (
	fieldMergableObject = 2 // MergableDataProto.mergable_data_object
	fieldObjectData     = 3 // MergableDataObject.mergeable_data_object_data

	fieldDataEntry    = 3 // MergeableDataObjectData.mergeable_data_object_entry
	fieldDataKeyItem  = 4 // …key_item (string)
	fieldDataTypeItem = 5 // …type_item (string)
	fieldDataUUIDItem = 6 // …uuid_item (bytes)

	fieldEntryDictionary = 6  // MergeableDataObjectEntry.dictionary
	fieldEntryNote       = 10 // MergeableDataObjectEntry.note
	fieldEntryCustomMap  = 13 // MergeableDataObjectEntry.custom_map
	fieldEntryOrderedSet = 16 // MergeableDataObjectEntry.ordered_set

	fieldMapType     = 1 // MergeableDataObjectMap.type
	fieldMapEntry    = 3 // MergeableDataObjectMap.map_entry
	fieldMapEntryKey = 1 // MapEntry.key
	fieldMapEntryVal = 2 // MapEntry.value (ObjectID)

	fieldObjectIDUint  = 2 // ObjectID.unsigned_integer_value
	fieldObjectIDIndex = 6 // ObjectID.object_index

	fieldOrderedSetOrdering = 1 // OrderedSet.ordering
	fieldOrderingArray      = 1 // OrderedSetOrdering.array
	fieldOrderingContents   = 2 // OrderedSetOrdering.contents (Dictionary)
	fieldArrayAttachment    = 2 // OrderedSetOrderingArray.attachment
	fieldAttachmentUUID     = 2 // OrderedSetOrderingArrayAttachment.uuid

	fieldDictElement = 1 // Dictionary.element
	fieldDictKey     = 1 // DictionaryElement.key (ObjectID)
	fieldDictValue   = 2 // DictionaryElement.value (ObjectID)

	fieldMergeNoteText = 2 // Note.note_text (cell content)
)

const tableRootType = "com.apple.notes.ICTable"

var errNoTableRoot = errors.New("table CRDT has no ICTable root")

// tableMarker resolves a table attachment's companion CRDT blob and renders it
// as a markdown pipe table. Missing bytes render as the honest "not captured"
// marker; a blob that fails to decode renders as "[table: unreadable]".
func tableMarker(uuid string, resolve TableResolver) string {
	if resolve == nil {
		return tableNotCaptured
	}
	zdata, ok := resolve(uuid)
	if !ok {
		return tableNotCaptured
	}
	md, err := decodeTable(zdata)
	if err != nil {
		return "[table: unreadable]"
	}
	return md
}

type tableCRDT struct {
	entries   []message
	keyItems  []string
	typeItems []string
	uuidIndex map[string]int
}

func decodeTable(zdata []byte) (string, error) {
	body, err := Inflate(zdata)
	if err != nil {
		return "", err
	}
	proto, err := parse(body)
	if err != nil {
		return "", fmt.Errorf("table proto: %w", err)
	}
	object, ok, err := proto.child(fieldMergableObject)
	if err != nil || !ok {
		return "", errors.New("table CRDT has no data object")
	}
	data, ok, err := object.child(fieldObjectData)
	if err != nil || !ok {
		return "", errors.New("table CRDT has no object data")
	}

	crdt := tableCRDT{
		keyItems:  data.repeatedStr(fieldDataKeyItem),
		typeItems: data.repeatedStr(fieldDataTypeItem),
		uuidIndex: map[string]int{},
	}
	for i, uuid := range data.repeated(fieldDataUUIDItem) {
		crdt.uuidIndex[string(uuid)] = i
	}
	for _, raw := range data.repeated(fieldDataEntry) {
		entry, err := parse(raw)
		if err != nil {
			return "", fmt.Errorf("table entry: %w", err)
		}
		crdt.entries = append(crdt.entries, entry)
	}
	return crdt.render()
}

func (t tableCRDT) render() (string, error) {
	root, ok := t.findRoot()
	if !ok {
		return "", errNoTableRoot
	}
	customMap, _, err := root.child(fieldEntryCustomMap)
	if err != nil {
		return "", err
	}

	var rowIndices, colIndices map[int]int
	var rows, cols int
	cellEntry := -1
	for _, raw := range customMap.repeated(fieldMapEntry) {
		entry, err := parse(raw)
		if err != nil {
			return "", err
		}
		keyIdx := int(entry.int32(fieldMapEntryKey, -1))
		valueID, _, err := entry.child(fieldMapEntryVal)
		if err != nil {
			return "", err
		}
		target := objectIndex(valueID)
		switch t.keyItem(keyIdx) {
		case "crRows":
			rowIndices, rows = t.parseOrdered(target)
		case "crColumns":
			colIndices, cols = t.parseOrdered(target)
		case "cellColumns":
			cellEntry = target
		}
	}
	if rows == 0 || cols == 0 {
		return "", errors.New("table CRDT has no cells")
	}

	grid := make([][]string, rows)
	for i := range grid {
		grid[i] = make([]string, cols)
	}
	if cellEntry >= 0 {
		t.parseCells(cellEntry, rowIndices, colIndices, grid)
	}
	return renderPipeTable(grid), nil
}

func (t tableCRDT) findRoot() (message, bool) {
	for _, entry := range t.entries {
		customMap, ok, err := entry.child(fieldEntryCustomMap)
		if err != nil || !ok {
			continue
		}
		typeIdx := int(customMap.int32(fieldMapType, -1))
		if t.typeItem(typeIdx) == tableRootType {
			return entry, true
		}
	}
	return nil, false
}

// parseOrdered reads an OrderedSet (rows or columns): the attachment array
// fixes the position of each UUID, and the contents dictionary aliases extra
// UUID indices onto those positions. It returns uuidIndex→position and a count.
func (t tableCRDT) parseOrdered(entryIndex int) (map[int]int, int) {
	indices := map[int]int{}
	entry, ok := t.entryAt(entryIndex)
	if !ok {
		return indices, 0
	}
	orderedSet, ok, err := entry.child(fieldEntryOrderedSet)
	if err != nil || !ok {
		return indices, 0
	}
	ordering, ok, err := orderedSet.child(fieldOrderedSetOrdering)
	if err != nil || !ok {
		return indices, 0
	}
	array, ok, err := ordering.child(fieldOrderingArray)
	if err != nil || !ok {
		return indices, 0
	}

	total := 0
	for _, raw := range array.repeated(fieldArrayAttachment) {
		attachment, err := parse(raw)
		if err != nil {
			continue
		}
		uuid, _ := attachment.bytes(fieldAttachmentUUID)
		if idx, ok := t.uuidIndex[string(uuid)]; ok {
			indices[idx] = total
		}
		total++
	}

	if contents, ok, _ := ordering.child(fieldOrderingContents); ok {
		for _, raw := range contents.repeated(fieldDictElement) {
			element, err := parse(raw)
			if err != nil {
				continue
			}
			keyUUID, okKey := t.targetUUID(element, fieldDictKey)
			valUUID, okVal := t.targetUUID(element, fieldDictValue)
			if okKey && okVal {
				if pos, ok := indices[keyUUID]; ok {
					indices[valUUID] = pos
				}
			}
		}
	}
	return indices, total
}

// parseCells fills grid[row][col] with cell text. Each cellColumns element
// points at a column and a nested dictionary of that column's rows; each of
// those points at a cell entry whose Note holds the cell text.
func (t tableCRDT) parseCells(entryIndex int, rowIndices, colIndices map[int]int, grid [][]string) {
	entry, ok := t.entryAt(entryIndex)
	if !ok {
		return
	}
	dict, ok, err := entry.child(fieldEntryDictionary)
	if err != nil || !ok {
		return
	}
	for _, raw := range dict.repeated(fieldDictElement) {
		column, err := parse(raw)
		if err != nil {
			continue
		}
		columnUUID, ok := t.targetUUID(column, fieldDictKey)
		if !ok {
			continue
		}
		col, ok := colIndices[columnUUID]
		if !ok {
			continue
		}
		rowDictEntry, ok := t.entryAt(t.childObjectIndex(column, fieldDictValue))
		if !ok {
			continue
		}
		rowDict, ok, err := rowDictEntry.child(fieldEntryDictionary)
		if err != nil || !ok {
			continue
		}
		for _, rowRaw := range rowDict.repeated(fieldDictElement) {
			rowElement, err := parse(rowRaw)
			if err != nil {
				continue
			}
			rowUUID, ok := t.targetUUID(rowElement, fieldDictKey)
			if !ok {
				continue
			}
			row, ok := rowIndices[rowUUID]
			if !ok {
				continue
			}
			cellEntry, ok := t.entryAt(t.childObjectIndex(rowElement, fieldDictValue))
			if !ok {
				continue
			}
			if note, ok, _ := cellEntry.child(fieldEntryNote); ok {
				text, _ := note.str(fieldMergeNoteText)
				if row < len(grid) && col < len(grid[row]) {
					grid[row][col] = text
				}
			}
		}
	}
}

// targetUUID follows a DictionaryElement key/value ObjectID to the entry it
// references, then returns that entry's own UUID index (custom_map →
// first map_entry → value.unsigned_integer_value).
func (t tableCRDT) targetUUID(element message, field uint64) (int, bool) {
	entry, ok := t.entryAt(t.childObjectIndex(element, field))
	if !ok {
		return 0, false
	}
	customMap, ok, err := entry.child(fieldEntryCustomMap)
	if err != nil || !ok {
		return 0, false
	}
	mapEntries := customMap.repeated(fieldMapEntry)
	if len(mapEntries) == 0 {
		return 0, false
	}
	first, err := parse(mapEntries[0])
	if err != nil {
		return 0, false
	}
	value, ok, err := first.child(fieldMapEntryVal)
	if err != nil || !ok {
		return 0, false
	}
	uuid, ok := value.varint(fieldObjectIDUint)
	return int(uuid), ok
}

func (t tableCRDT) childObjectIndex(m message, field uint64) int {
	child, ok, err := m.child(field)
	if err != nil || !ok {
		return -1
	}
	return objectIndex(child)
}

func (t tableCRDT) entryAt(index int) (message, bool) {
	if index < 0 || index >= len(t.entries) {
		return nil, false
	}
	return t.entries[index], true
}

func (t tableCRDT) keyItem(index int) string {
	if index < 0 || index >= len(t.keyItems) {
		return ""
	}
	return t.keyItems[index]
}

func (t tableCRDT) typeItem(index int) string {
	if index < 0 || index >= len(t.typeItems) {
		return ""
	}
	return t.typeItems[index]
}

func objectIndex(objectID message) int {
	if v, ok := objectID.varint(fieldObjectIDIndex); ok {
		return int(v)
	}
	return -1
}

func renderPipeTable(grid [][]string) string {
	if len(grid) == 0 || len(grid[0]) == 0 {
		return "[table]"
	}
	cols := len(grid[0])
	var b strings.Builder
	writeRow := func(cells []string) {
		b.WriteString("|")
		for _, cell := range cells {
			b.WriteString(" ")
			b.WriteString(escapeCell(cell))
			b.WriteString(" |")
		}
		b.WriteString("\n")
	}
	writeRow(grid[0])
	b.WriteString("|")
	for i := 0; i < cols; i++ {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, row := range grid[1:] {
		writeRow(row)
	}
	return strings.TrimRight(b.String(), "\n")
}

func escapeCell(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "|", "\\|")
	return strings.TrimSpace(text)
}
