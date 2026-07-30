package archive

const unreadLabelID = "UNREAD"

func isUnread(labelIDs []string) bool {
	for _, labelID := range labelIDs {
		if labelID == unreadLabelID {
			return true
		}
	}
	return false
}

func boolToInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
