package dto

import "biosample-cold-custody-tracking/backend/internal/constants"

type CreateTemperatureExceptionRequest struct {
	ExceptionNo       string                                  `json:"exceptionNo" binding:"required,min=3,max=50"`
	ContainerID       uint                                    `json:"containerId" binding:"required"`
	StartTemperatureC float64                                 `json:"startTemperatureC" binding:"gte=-210,lte=40"`
	AlarmReason       string                                  `json:"alarmReason" binding:"required,min=3,max=1000"`
	HandlerName       string                                  `json:"handlerName" binding:"omitempty,min=2,max=100"`
	Action            constants.TemperatureExceptionAction    `json:"action" binding:"required,oneof=onsite relocation"`
	Items             []CreateTemperatureExceptionItemRequest `json:"items" binding:"required,min=1,dive"`
}

type CreateTemperatureExceptionItemRequest struct {
	SpecimenID        uint   `json:"specimenId" binding:"required"`
	TargetContainerID *uint  `json:"targetContainerId"`
	TargetPosition    string `json:"targetPosition" binding:"omitempty,max=120"`
	Notes             string `json:"notes" binding:"omitempty,max=1000"`
}

type CloseTemperatureExceptionRequest struct {
	EndTemperatureC float64                               `json:"endTemperatureC" binding:"gte=-210,lte=40"`
	Outcome         constants.TemperatureExceptionOutcome `json:"outcome" binding:"required,oneof=recovered discarded"`
	ConclusionNotes string                                `json:"conclusionNotes" binding:"required,min=3,max=2000"`
	HandlerName     string                                `json:"handlerName" binding:"omitempty,min=2,max=100"`
}
