package calendarstore

type Data struct {
	SourcePath       string
	SourceModifiedAt string
	Calendars        []Calendar
	Events           []Event
}

type Calendar struct {
	RowID         int64
	StoreID       int64
	Title         string
	Type          int64
	ExternalID    string
	StoreName     string
	StoreType     int64
	StoreDisabled bool
}

type Event struct {
	RowID            int64
	UUID             string
	UniqueIdentifier string
	Summary          string
	Description      string
	Start            EventTime
	End              EventTime
	AllDay           bool
	Calendar         Calendar
	Organizer        Person
	Status           string
	URL              string
	HasRecurrences   bool
	Availability     *int64
	Location         Location
	Attendees        []Participant
}

type EventTime struct {
	Value string
	Unix  int64
}

type Location struct {
	Title   string
	Address string
}

type Person struct {
	DisplayName string
	Email       string
	PhoneNumber string
	Address     string
}

type Participant struct {
	DisplayName string
	Email       string
	PhoneNumber string
	Address     string
	RSVPStatus  string
	Role        string
	Self        bool
	Comment     string
}
