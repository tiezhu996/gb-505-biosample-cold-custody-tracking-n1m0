package model

import (
	"fmt"
	"strings"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

var exceptionNumberPattern = transferNumberPattern

// TemperatureException 是冻存柜温度异常处置单。处置单未结案期间，
// 受影响样本暂缓交接与协议放行。
type TemperatureException struct {
	Base
	ExceptionNo       string                                `gorm:"size:50;uniqueIndex;not null" json:"exceptionNo"`
	ContainerID       uint                                  `gorm:"index;not null" json:"containerId"`
	Container         *StorageContainer                     `gorm:"foreignKey:ContainerID" json:"container,omitempty"`
	StartTemperatureC float64                               `gorm:"type:numeric(6,2);not null" json:"startTemperatureC"`
	AlarmReason       string                                `gorm:"size:1000;not null" json:"alarmReason"`
	HandlerName       string                                `gorm:"size:100;not null" json:"handlerName"`
	Action            constants.TemperatureExceptionAction  `gorm:"size:24;not null" json:"action"`
	State             constants.TemperatureExceptionState   `gorm:"size:20;index;not null;default:'open'" json:"state"`
	EndTemperatureC   *float64                              `gorm:"type:numeric(6,2)" json:"endTemperatureC,omitempty"`
	Outcome           constants.TemperatureExceptionOutcome `gorm:"size:24" json:"outcome,omitempty"`
	ConclusionNotes   string                                `gorm:"size:2000" json:"conclusionNotes,omitempty"`
	ClosedByName      string                                `gorm:"size:100" json:"closedByName,omitempty"`
	StartedAt         time.Time                             `gorm:"index;not null" json:"startedAt"`
	ClosedAt          *time.Time                            `gorm:"index" json:"closedAt,omitempty"`
	Items             []TemperatureExceptionItem            `gorm:"foreignKey:ExceptionID" json:"items,omitempty"`
}

// TemperatureExceptionItem 记录异常期间容器内每支样本的暴露情况；
// 选择转柜时逐支登记目标格位。
type TemperatureExceptionItem struct {
	Base
	ExceptionID           uint                  `gorm:"uniqueIndex:idx_exception_specimen;index;not null" json:"exceptionId"`
	Exception             *TemperatureException `gorm:"foreignKey:ExceptionID" json:"exception,omitempty"`
	SpecimenID            uint                  `gorm:"uniqueIndex:idx_exception_specimen;not null" json:"specimenId"`
	Specimen              *Specimen             `gorm:"foreignKey:SpecimenID" json:"specimen,omitempty"`
	TargetContainerID     *uint                 `gorm:"index" json:"targetContainerId,omitempty"`
	TargetContainer       *StorageContainer     `gorm:"foreignKey:TargetContainerID" json:"targetContainer,omitempty"`
	TargetPosition        string                `gorm:"size:120" json:"targetPosition,omitempty"`
	Moved                 bool                  `gorm:"not null;default:false" json:"moved"`
	SourcePosition        string                `gorm:"size:120" json:"sourcePosition,omitempty"`
	SourceTemperatureZone string                `gorm:"size:32" json:"sourceTemperatureZone,omitempty"`
	Notes                 string                `gorm:"size:1000" json:"notes,omitempty"`
}

func (e *TemperatureException) Normalize() {
	e.ExceptionNo = strings.ToUpper(strings.TrimSpace(e.ExceptionNo))
	e.AlarmReason = strings.TrimSpace(e.AlarmReason)
	e.HandlerName = strings.TrimSpace(e.HandlerName)
	e.ClosedByName = strings.TrimSpace(e.ClosedByName)
	e.ConclusionNotes = strings.TrimSpace(e.ConclusionNotes)
	if e.State == "" {
		e.State = constants.TemperatureExceptionOpen
	}
}

func (e TemperatureException) Validate() error {
	if !exceptionNumberPattern.MatchString(e.ExceptionNo) {
		return fmt.Errorf("exception number must contain 3-50 uppercase letters, numbers or hyphens")
	}
	if e.ContainerID == 0 {
		return fmt.Errorf("storage container is required")
	}
	if e.StartTemperatureC < -210 || e.StartTemperatureC > 40 {
		return fmt.Errorf("start temperature must be between -210 and 40 Celsius")
	}
	if length := len([]rune(e.AlarmReason)); length < 3 || length > 1000 {
		return fmt.Errorf("alarm reason must contain 3-1000 characters")
	}
	if length := len([]rune(e.HandlerName)); length < 2 || length > 100 {
		return fmt.Errorf("handler must contain 2-100 characters")
	}
	if !e.Action.Valid() {
		return fmt.Errorf("unsupported exception action: %s", e.Action)
	}
	if !e.State.Valid() {
		return fmt.Errorf("unsupported exception state: %s", e.State)
	}
	if e.StartedAt.IsZero() {
		return fmt.Errorf("alarm start time is required")
	}
	if e.State == constants.TemperatureExceptionOpen {
		if e.ClosedAt != nil || e.EndTemperatureC != nil || e.Outcome != "" || e.ClosedByName != "" || e.ConclusionNotes != "" {
			return fmt.Errorf("open exception cannot contain closure metadata")
		}
		return nil
	}
	if e.ClosedAt == nil || e.EndTemperatureC == nil || !e.Outcome.Valid() {
		return fmt.Errorf("closed exception requires closure time, end temperature and outcome")
	}
	if *e.EndTemperatureC < -210 || *e.EndTemperatureC > 40 {
		return fmt.Errorf("end temperature must be between -210 and 40 Celsius")
	}
	if length := len([]rune(e.ConclusionNotes)); length < 3 || length > 2000 {
		return fmt.Errorf("conclusion must contain 3-2000 characters")
	}
	if length := len([]rune(e.ClosedByName)); length < 2 || length > 100 {
		return fmt.Errorf("closing handler must contain 2-100 characters")
	}
	return nil
}

func (e TemperatureException) Open() bool {
	return e.State == constants.TemperatureExceptionOpen
}

func (i TemperatureExceptionItem) Validate() error {
	if i.SpecimenID == 0 {
		return fmt.Errorf("exception item requires a specimen")
	}
	if len([]rune(i.TargetPosition)) > 120 {
		return fmt.Errorf("target position cannot exceed 120 characters")
	}
	if len([]rune(i.Notes)) > 1000 {
		return fmt.Errorf("exception item notes cannot exceed 1000 characters")
	}
	if i.Moved {
		if i.TargetContainerID == nil || *i.TargetContainerID == 0 || i.TargetPosition == "" {
			return fmt.Errorf("relocated specimen requires target container and position")
		}
	}
	return nil
}
