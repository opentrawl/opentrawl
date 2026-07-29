package model

import "time"

type SourceContact struct {
	Source                                                string              `json:"source"`
	ExternalID                                            string              `json:"external_id,omitempty"`
	Name                                                  string              `json:"name"`
	Tags                                                  []string            `json:"tags,omitempty"`
	Emails                                                []ContactValue      `json:"emails,omitempty"`
	Phones                                                []ContactValue      `json:"phones,omitempty"`
	Addresses                                             []ContactValue      `json:"addresses,omitempty"`
	Avatar                                                *SourceAvatar       `json:"avatar,omitempty"`
	Accounts                                              map[string][]string `json:"accounts,omitempty"`
	LatestArchiveRecordTimeInvolvingPersonInSourceArchive time.Time           `json:"latest_archive_record_time_involving_person_in_source_archive,omitzero"`
	MessageCountInvolvingPersonInSourceArchive            uint64              `json:"message_count_involving_person_in_source_archive,omitempty"`
	ETag                                                  string              `json:"etag,omitempty"`
}

type SourceAvatar struct {
	Data   []byte `json:"-"`
	MIME   string `json:"mime,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	URL    string `json:"url,omitempty"`
}
