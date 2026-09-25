package dto

import "time"

type CreateIncidentRequest struct {
	ContainerID uint       `json:"containerId" binding:"required"`
	StartTempC  *float64   `json:"startTempC" binding:"required,gte=-210,lte=40"`
	AlarmReason string     `json:"alarmReason" binding:"required,min=3,max=500"`
	StartedAt   *time.Time `json:"startedAt"`
}

type IncidentRelocationItem struct {
	SpecimenID        uint   `json:"specimenId" binding:"required"`
	TargetContainerID uint   `json:"targetContainerId" binding:"required"`
	TargetPosition    string `json:"targetPosition" binding:"required,min=1,max=120"`
}

type RelocateIncidentRequest struct {
	Items []IncidentRelocationItem `json:"items" binding:"required,min=1,dive"`
}

type ResolveIncidentRequest struct {
	EndTempC   *float64   `json:"endTempC" binding:"required,gte=-210,lte=40"`
	Conclusion string     `json:"conclusion" binding:"required,min=3,max=1000"`
	ResolvedAt *time.Time `json:"resolvedAt"`
}
