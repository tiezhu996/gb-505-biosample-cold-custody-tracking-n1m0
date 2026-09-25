package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

var incidentNumberPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{2,49}$`)

// TemperatureIncident is the temperature exception handling order opened when
// a storage container alarms. While an order is open the samples under it are
// frozen: custody handovers and protocol release approvals are suspended until
// the order is closed.
type TemperatureIncident struct {
	Base
	IncidentNo      string                    `gorm:"size:50;uniqueIndex;not null" json:"incidentNo"`
	ContainerID     uint                      `gorm:"index;not null" json:"containerId"`
	Container       *StorageContainer         `json:"container,omitempty"`
	State           constants.IncidentState   `gorm:"size:20;index;not null;default:'open'" json:"state"`
	StartTempC      float64                   `gorm:"type:numeric(6,2);not null" json:"startTempC"`
	EndTempC        *float64                  `gorm:"type:numeric(6,2)" json:"endTempC,omitempty"`
	AlarmReason     string                    `gorm:"size:500;not null" json:"alarmReason"`
	Conclusion      string                    `gorm:"size:1000" json:"conclusion,omitempty"`
	HandlerID       uint                      `gorm:"index;not null" json:"handlerId"`
	HandlerName     string                    `gorm:"size:100;not null" json:"handlerName"`
	RecoveredByID   *uint                     `gorm:"index" json:"recoveredById,omitempty"`
	RecoveredByName string                    `gorm:"size:100" json:"recoveredByName,omitempty"`
	StartedAt       time.Time                 `gorm:"index;not null" json:"startedAt"`
	ResolvedAt      *time.Time                `gorm:"index" json:"resolvedAt,omitempty"`
	Items           []TemperatureIncidentItem `gorm:"foreignKey:IncidentID" json:"items,omitempty"`
}

func (i *TemperatureIncident) Normalize() {
	i.IncidentNo = strings.ToUpper(strings.TrimSpace(i.IncidentNo))
	i.AlarmReason = strings.TrimSpace(i.AlarmReason)
	i.Conclusion = strings.TrimSpace(i.Conclusion)
	i.HandlerName = strings.TrimSpace(i.HandlerName)
	i.RecoveredByName = strings.TrimSpace(i.RecoveredByName)
	if i.State == "" {
		i.State = constants.IncidentStateOpen
	}
}

func (i TemperatureIncident) Validate() error {
	if !incidentNumberPattern.MatchString(i.IncidentNo) {
		return fmt.Errorf("incident number must contain 3-50 uppercase letters, numbers or hyphens")
	}
	if i.ContainerID == 0 {
		return fmt.Errorf("container is required")
	}
	if !i.State.Valid() {
		return fmt.Errorf("unsupported incident state: %s", i.State)
	}
	if i.StartTempC < -210 || i.StartTempC > 40 {
		return fmt.Errorf("start temperature must be between -210 and 40 Celsius")
	}
	if length := len([]rune(i.AlarmReason)); length < 3 || length > 500 {
		return fmt.Errorf("alarm reason must contain 3-500 characters")
	}
	if len([]rune(i.Conclusion)) > 1000 {
		return fmt.Errorf("conclusion cannot exceed 1000 characters")
	}
	if i.HandlerID == 0 || i.HandlerName == "" || i.StartedAt.IsZero() {
		return fmt.Errorf("handler identity and alarm start time are required")
	}
	if len([]rune(i.HandlerName)) > 100 || len([]rune(i.RecoveredByName)) > 100 {
		return fmt.Errorf("handler name cannot exceed 100 characters")
	}
	if i.State == constants.IncidentStateOpen {
		if i.ResolvedAt != nil || i.RecoveredByID != nil || i.RecoveredByName != "" || i.EndTempC != nil {
			return fmt.Errorf("open incident cannot contain recovery metadata")
		}
		return nil
	}
	if i.ResolvedAt == nil || i.RecoveredByID == nil || *i.RecoveredByID == 0 ||
		i.RecoveredByName == "" || i.EndTempC == nil {
		return fmt.Errorf("resolved incident requires recovery identity, end temperature and time")
	}
	if len([]rune(i.Conclusion)) < 3 {
		return fmt.Errorf("resolved incident requires a conclusion of at least 3 characters")
	}
	return nil
}

// TemperatureIncidentItem records one sample caught by an exception order. It
// keeps the container/position snapshot captured when the alarm was registered;
// relocation, if chosen, fills in the target coordinates and move time.
type TemperatureIncidentItem struct {
	Base
	IncidentID          uint                 `gorm:"index;not null;uniqueIndex:idx_incident_specimen" json:"incidentId"`
	Incident            *TemperatureIncident `gorm:"foreignKey:IncidentID" json:"incident,omitempty"`
	SpecimenID          uint                 `gorm:"index;not null;uniqueIndex:idx_incident_specimen" json:"specimenId"`
	Specimen            Specimen             `json:"specimen,omitempty"`
	SnapshotContainerID uint                 `gorm:"index" json:"snapshotContainerId,omitempty"`
	SnapshotPosition    string               `gorm:"size:120" json:"snapshotPosition,omitempty"`
	TargetContainerID   *uint                `gorm:"index" json:"targetContainerId,omitempty"`
	TargetContainer     *StorageContainer    `gorm:"foreignKey:TargetContainerID" json:"targetContainer,omitempty"`
	TargetPosition      string               `gorm:"size:120" json:"targetPosition,omitempty"`
	RelocatedAt         *time.Time           `gorm:"index" json:"relocatedAt,omitempty"`
	RelocatedByName     string               `gorm:"size:100" json:"relocatedByName,omitempty"`
}

func (item TemperatureIncidentItem) Validate() error {
	if item.IncidentID == 0 || item.SpecimenID == 0 {
		return fmt.Errorf("incident and specimen are required")
	}
	if len([]rune(item.SnapshotPosition)) > 120 || len([]rune(item.TargetPosition)) > 120 {
		return fmt.Errorf("position cannot exceed 120 characters")
	}
	if len([]rune(item.RelocatedByName)) > 100 {
		return fmt.Errorf("relocation handler name cannot exceed 100 characters")
	}
	hasTarget := item.TargetContainerID != nil || item.TargetPosition != "" || item.RelocatedAt != nil
	hasAll := item.TargetContainerID != nil && *item.TargetContainerID != 0 &&
		strings.TrimSpace(item.TargetPosition) != "" && item.RelocatedAt != nil &&
		strings.TrimSpace(item.RelocatedByName) != ""
	if hasTarget != hasAll {
		return fmt.Errorf("relocation requires target container, target position, handler and time together")
	}
	return nil
}

func (item TemperatureIncidentItem) Relocated() bool {
	return item.TargetContainerID != nil && *item.TargetContainerID != 0 && item.RelocatedAt != nil
}
