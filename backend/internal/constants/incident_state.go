package constants

type IncidentState string

const (
	IncidentStateOpen     IncidentState = "open"
	IncidentStateResolved IncidentState = "resolved"
)

func IncidentStates() []IncidentState {
	return []IncidentState{IncidentStateOpen, IncidentStateResolved}
}

func (s IncidentState) Valid() bool {
	switch s {
	case IncidentStateOpen, IncidentStateResolved:
		return true
	default:
		return false
	}
}

func (s IncidentState) Open() bool { return s == IncidentStateOpen }
