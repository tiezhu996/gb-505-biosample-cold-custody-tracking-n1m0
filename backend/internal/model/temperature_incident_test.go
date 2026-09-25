package model

import (
	"testing"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

func validIncident() TemperatureIncident {
	return TemperatureIncident{
		IncidentNo:  "TI-20260925-001",
		ContainerID: 2,
		State:       constants.IncidentStateOpen,
		StartTempC:  -58.6,
		AlarmReason: "柜门密封条老化导致库温回升",
		HandlerID:   3,
		HandlerName: "冻存保管员",
		StartedAt:   time.Now().Add(-time.Hour),
	}
}

func TestOpenIncidentRejectsRecoveryMetadata(t *testing.T) {
	incident := validIncident()
	if err := incident.Validate(); err != nil {
		t.Fatalf("valid open incident rejected: %v", err)
	}
	temperature := -79.1
	incident.EndTempC = &temperature
	if err := incident.Validate(); err == nil {
		t.Fatal("open incident with end temperature must be rejected")
	}
}

func TestResolvedIncidentRequiresConclusionAndEndTemp(t *testing.T) {
	incident := validIncident()
	resolvedAt := time.Now()
	recoveredBy := uint(3)
	incident.State = constants.IncidentStateResolved
	incident.RecoveredByID = &recoveredBy
	incident.RecoveredByName = "冻存保管员"
	incident.ResolvedAt = &resolvedAt
	if err := incident.Validate(); err == nil {
		t.Fatal("resolved incident without end temperature and conclusion must fail")
	}
	endTemp := -79.0
	incident.EndTempC = &endTemp
	incident.Conclusion = "库温恢复正常，样本复核后继续冻存"
	if err := incident.Validate(); err != nil {
		t.Fatalf("valid resolved incident rejected: %v", err)
	}
}

func TestIncidentItemRequiresCompleteRelocation(t *testing.T) {
	partial := TemperatureIncidentItem{IncidentID: 1, SpecimenID: 9, TargetPosition: "R01-B01-A01"}
	if err := partial.Validate(); err == nil {
		t.Fatal("relocation target without container and move time must be rejected")
	}
	containerID := uint(5)
	movedAt := time.Now()
	complete := TemperatureIncidentItem{
		IncidentID: 1, SpecimenID: 9,
		TargetContainerID: &containerID, TargetPosition: "R01-B01-A01",
		RelocatedAt: &movedAt, RelocatedByName: "冻存保管员",
	}
	if err := complete.Validate(); err != nil {
		t.Fatalf("complete relocation item rejected: %v", err)
	}
	if !complete.Relocated() {
		t.Fatal("complete relocation item must be reported as relocated")
	}
}

func TestRelocationZoneMustNotBeWarmer(t *testing.T) {
	if CanRelocateZone("minus80", "minus20") {
		t.Fatal("minus80 samples must not be relocated to a warmer minus20 zone")
	}
	if !CanRelocateZone("minus80", "liquid_nitrogen") {
		t.Fatal("minus80 samples may be relocated to a colder liquid nitrogen zone")
	}
	if !CanRelocateZone("minus80", "minus80") {
		t.Fatal("same-zone relocation between minus80 containers must be allowed")
	}
	if CanRelocateZone("minus80", "unknown") {
		t.Fatal("unknown target zone must be rejected")
	}
}
