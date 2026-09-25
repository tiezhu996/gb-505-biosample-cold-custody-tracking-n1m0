package constants

type TemperatureExceptionState string

const (
	TemperatureExceptionOpen   TemperatureExceptionState = "open"
	TemperatureExceptionClosed TemperatureExceptionState = "closed"
)

func (s TemperatureExceptionState) Valid() bool {
	switch s {
	case TemperatureExceptionOpen, TemperatureExceptionClosed:
		return true
	default:
		return false
	}
}

type TemperatureExceptionAction string

const (
	TemperatureActionOnsite     TemperatureExceptionAction = "onsite"
	TemperatureActionRelocation TemperatureExceptionAction = "relocation"
)

func (a TemperatureExceptionAction) Valid() bool {
	switch a {
	case TemperatureActionOnsite, TemperatureActionRelocation:
		return true
	default:
		return false
	}
}

type TemperatureExceptionOutcome string

const (
	TemperatureOutcomeRecovered TemperatureExceptionOutcome = "recovered"
	TemperatureOutcomeDiscarded TemperatureExceptionOutcome = "discarded"
)

func (o TemperatureExceptionOutcome) Valid() bool {
	switch o {
	case TemperatureOutcomeRecovered, TemperatureOutcomeDiscarded:
		return true
	default:
		return false
	}
}
