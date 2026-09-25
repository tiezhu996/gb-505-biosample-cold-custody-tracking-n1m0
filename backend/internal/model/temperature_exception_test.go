package model

import (
	"strings"
	"testing"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

func TestOpenTemperatureExceptionValidation(t *testing.T) {
	exception := TemperatureException{
		ExceptionNo:       "TE-20260925-001",
		ContainerID:       3,
		StartTemperatureC: -58.2,
		AlarmReason:       "柜门未关严，温度持续回升",
		HandlerName:       "冻存保管员",
		Action:            constants.TemperatureActionOnsite,
		State:             constants.TemperatureExceptionOpen,
		StartedAt:         time.Now().Add(-time.Hour),
	}
	if err := exception.Validate(); err != nil {
		t.Fatalf("valid open exception rejected: %v", err)
	}
	exception.EndTemperatureC = floatPointer(-79.1)
	if err := exception.Validate(); err == nil {
		t.Fatal("open exception with closure metadata must be rejected")
	}
}

func TestClosedTemperatureExceptionValidation(t *testing.T) {
	end := -79.4
	closedAt := time.Now()
	exception := TemperatureException{
		ExceptionNo:       "TE-20260925-002",
		ContainerID:       3,
		StartTemperatureC: -57.0,
		AlarmReason:       "制冷模块告警",
		HandlerName:       "冻存保管员",
		Action:            constants.TemperatureActionRelocation,
		State:             constants.TemperatureExceptionClosed,
		EndTemperatureC:   &end,
		Outcome:           constants.TemperatureOutcomeRecovered,
		ConclusionNotes:   "更换制冷模块后温度恢复，样本抽检正常",
		ClosedByName:      "冻存保管员",
		StartedAt:         time.Now().Add(-2 * time.Hour),
		ClosedAt:          &closedAt,
	}
	if err := exception.Validate(); err != nil {
		t.Fatalf("valid closed exception rejected: %v", err)
	}
	exception.ConclusionNotes = ""
	if err := exception.Validate(); err == nil {
		t.Fatal("closed exception without conclusion must be rejected")
	}
	exception.ConclusionNotes = strings.Repeat("长", 2001)
	if err := exception.Validate(); err == nil {
		t.Fatal("conclusion longer than 2000 characters must be rejected")
	}
}

func TestRelocationItemRequiresTarget(t *testing.T) {
	item := TemperatureExceptionItem{ExceptionID: 1, SpecimenID: 9, Moved: true}
	if err := item.Validate(); err == nil {
		t.Fatal("relocated item without target slot must be rejected")
	}
	containerID := uint(4)
	item.TargetContainerID = &containerID
	item.TargetPosition = "R01-BX02-A09"
	if err := item.Validate(); err != nil {
		t.Fatalf("valid relocation item rejected: %v", err)
	}
}

func floatPointer(value float64) *float64 { return &value }
