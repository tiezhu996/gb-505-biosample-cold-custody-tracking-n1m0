package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
)

var (
	ErrIncidentAlreadyOpen      = errors.New("container already has an open temperature incident")
	ErrIncidentNotOpen          = errors.New("temperature incident is not open")
	ErrIncidentItemMismatch     = errors.New("relocation item does not belong to the open incident")
	ErrIncidentSpecimenMoved    = errors.New("incident specimen custody changed after the alarm")
	ErrIncidentAlreadyRelocated = errors.New("incident specimen has already been relocated")
	ErrIncidentTargetSame       = errors.New("relocation target container must differ from the alarm container")
	ErrIncidentZoneUnfit        = errors.New("relocation target temperature zone is not cold enough")
)

type IncidentFilter struct {
	dto.PageQuery
	State       constants.IncidentState `form:"state"`
	ContainerID uint                    `form:"containerId"`
}

type IncidentRelocationPlanItem struct {
	SpecimenID        uint
	TargetContainerID uint
	TargetPosition    string
	OperatorID        uint
	OperatorName      string
	MovedAt           time.Time
}

type IncidentRelocationPlan struct {
	Items []IncidentRelocationPlanItem
}

type IncidentResolution struct {
	EndTempC       float64
	Conclusion     string
	ResolvedByID   uint
	ResolvedByName string
	ResolvedAt     time.Time
}

type IncidentRepository interface {
	List(context.Context, IncidentFilter) ([]model.TemperatureIncident, int64, error)
	Find(context.Context, uint) (*model.TemperatureIncident, error)
	FindByNumber(context.Context, string) (*model.TemperatureIncident, error)
	CountToday(context.Context, time.Time) (int64, error)
	Create(context.Context, *model.TemperatureIncident) error
	Relocate(context.Context, uint, IncidentRelocationPlan) (*model.TemperatureIncident, error)
	Resolve(context.Context, uint, IncidentResolution) (*model.TemperatureIncident, error)
	CountOpenForSpecimen(context.Context, uint) (int64, error)
	CountOpenForContainer(context.Context, uint) (int64, error)
	CountOpenForSpecimenTx(*gorm.DB, uint) (int64, error)
	CountOpenForContainerTx(*gorm.DB, uint) (int64, error)
}

type incidentRepository struct{ db *gorm.DB }

func NewIncidentRepository(db *gorm.DB) IncidentRepository {
	return &incidentRepository{db: db}
}

func incidentPreloads(db *gorm.DB) *gorm.DB {
	return db.
		Preload("Container").
		Preload("Items.Specimen").
		Preload("Items.Specimen.StorageContainer").
		Preload("Items.TargetContainer")
}

func (r *incidentRepository) List(ctx context.Context, filter IncidentFilter) ([]model.TemperatureIncident, int64, error) {
	query := filter.PageQuery.Normalize()
	db := r.db.WithContext(ctx).Model(&model.TemperatureIncident{})
	if state := strings.TrimSpace(string(filter.State)); state != "" {
		db = db.Where("state = ?", state)
	}
	if filter.ContainerID > 0 {
		db = db.Where("container_id = ?", filter.ContainerID)
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		like := "%" + search + "%"
		db = db.Where("incident_no ILIKE ? OR alarm_reason ILIKE ? OR handler_name ILIKE ? OR conclusion ILIKE ?", like, like, like, like)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.TemperatureIncident, 0)
	err := incidentPreloads(db).Order("started_at DESC, id DESC").
		Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&items).Error
	return items, total, err
}

func (r *incidentRepository) Find(ctx context.Context, id uint) (*model.TemperatureIncident, error) {
	var item model.TemperatureIncident
	err := incidentPreloads(r.db.WithContext(ctx)).First(&item, id).Error
	return &item, err
}

func (r *incidentRepository) FindByNumber(ctx context.Context, number string) (*model.TemperatureIncident, error) {
	var item model.TemperatureIncident
	err := incidentPreloads(r.db.WithContext(ctx)).Where("incident_no = ?", strings.TrimSpace(number)).First(&item).Error
	return &item, err
}

func (r *incidentRepository) CountToday(ctx context.Context, dayStart time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.TemperatureIncident{}).
		Where("started_at >= ?", dayStart).Count(&count).Error
	return count, err
}

func (r *incidentRepository) Create(ctx context.Context, incident *model.TemperatureIncident) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var container model.StorageContainer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&container, incident.ContainerID).Error; err != nil {
			return err
		}
		var openCount int64
		if err := tx.Model(&model.TemperatureIncident{}).
			Where("container_id = ? AND state = ?", incident.ContainerID, constants.IncidentStateOpen).
			Count(&openCount).Error; err != nil {
			return err
		}
		if openCount > 0 {
			return ErrIncidentAlreadyOpen
		}
		var specimens []model.Specimen
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("storage_container_id = ? AND state NOT IN ?", incident.ContainerID,
				[]constants.SpecimenState{constants.SpecimenStateReleased, constants.SpecimenStateDisposed}).
			Find(&specimens).Error; err != nil {
			return err
		}
		incident.Items = make([]model.TemperatureIncidentItem, 0, len(specimens))
		for _, specimen := range specimens {
			snapshotContainer := specimen.StorageContainerID
			incident.Items = append(incident.Items, model.TemperatureIncidentItem{
				SpecimenID:          specimen.ID,
				SnapshotContainerID: derefUint(snapshotContainer),
				SnapshotPosition:    specimen.Position,
			})
		}
		if err := tx.Create(incident).Error; err != nil {
			return err
		}
		return tx.Model(&model.StorageContainer{}).Where("id = ?", container.ID).
			UpdateColumn("status", "alarm").Error
	})
}

func (r *incidentRepository) Relocate(ctx context.Context, incidentID uint, plan IncidentRelocationPlan) (*model.TemperatureIncident, error) {
	var incident model.TemperatureIncident
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&incident, incidentID).Error; err != nil {
			return err
		}
		if incident.State != constants.IncidentStateOpen {
			return ErrIncidentNotOpen
		}

		// Load the items covered by this order with a row lock.
		itemLookup := make(map[uint]*model.TemperatureIncidentItem)
		for _, planned := range plan.Items {
			if _, repeated := itemLookup[planned.SpecimenID]; repeated {
				return ErrIncidentItemMismatch
			}
			var item model.TemperatureIncidentItem
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("incident_id = ? AND specimen_id = ?", incidentID, planned.SpecimenID).
				First(&item).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrIncidentItemMismatch
				}
				return err
			}
			if item.Relocated() {
				return ErrIncidentAlreadyRelocated
			}
			itemLookup[planned.SpecimenID] = &item
		}

		// Pre-check every target container: lock, capacity, zone and unique positions.
		// Any single failure invalidates the whole relocation order.
		targetIDs := make(map[uint]struct{})
		for _, planned := range plan.Items {
			if planned.TargetContainerID == incident.ContainerID {
				return ErrIncidentTargetSame
			}
			targetIDs[planned.TargetContainerID] = struct{}{}
		}
		targetContainers := make(map[uint]*model.StorageContainer)
		for containerID := range targetIDs {
			var target model.StorageContainer
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, containerID).Error; err != nil {
				return err
			}
			if !target.Active || target.Status != "available" {
				return ErrTargetContainerFull
			}
			targetContainers[target.ID] = &target
		}
		var source model.StorageContainer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&source, incident.ContainerID).Error; err != nil {
			return err
		}
		inboundCount := make(map[uint]int)
		targetPositions := make(map[uint]map[string]struct{})
		for _, planned := range plan.Items {
			target := targetContainers[planned.TargetContainerID]
			if !model.CanRelocateZone(source.TemperatureZone, target.TemperatureZone) {
				return ErrIncidentZoneUnfit
			}
			position := strings.TrimSpace(planned.TargetPosition)
			var occupied int64
			if err := tx.Model(&model.Specimen{}).
				Where("storage_container_id = ? AND position = ? AND state NOT IN ?",
					target.ID, position,
					[]constants.SpecimenState{constants.SpecimenStateReleased, constants.SpecimenStateDisposed}).
				Count(&occupied).Error; err != nil {
				return err
			}
			if occupied > 0 {
				return ErrPositionOccupied
			}
			if targetPositions[target.ID] == nil {
				targetPositions[target.ID] = make(map[string]struct{})
			}
			if _, duplicate := targetPositions[target.ID][position]; duplicate {
				return ErrPositionOccupied
			}
			targetPositions[target.ID][position] = struct{}{}
			inboundCount[target.ID]++
		}
		for containerID, target := range targetContainers {
			if target.Occupied+inboundCount[containerID] > target.Capacity {
				return ErrTargetContainerFull
			}
		}

		// Move the specimens one by one while they remain locked.
		for _, planned := range plan.Items {
			var specimen model.Specimen
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&specimen, planned.SpecimenID).Error; err != nil {
				return err
			}
			if specimen.StorageContainerID == nil || *specimen.StorageContainerID != incident.ContainerID {
				return ErrIncidentSpecimenMoved
			}
			target := targetContainers[planned.TargetContainerID]
			specimen.StorageContainerID = &target.ID
			specimen.Position = strings.TrimSpace(planned.TargetPosition)
			if specimen.State == constants.SpecimenStateReceived || specimen.State == constants.SpecimenStateAliquoted {
				specimen.State = constants.SpecimenStateStored
			}
			if err := specimen.Validate(); err != nil {
				return err
			}
			if err := tx.Save(&specimen).Error; err != nil {
				return err
			}
			item := itemLookup[planned.SpecimenID]
			item.TargetContainerID = &target.ID
			item.TargetPosition = strings.TrimSpace(planned.TargetPosition)
			item.RelocatedAt = &planned.MovedAt
			item.RelocatedByName = strings.TrimSpace(planned.OperatorName)
			if err := item.Validate(); err != nil {
				return err
			}
			if err := tx.Save(item).Error; err != nil {
				return err
			}
		}

		for containerID, added := range inboundCount {
			result := tx.Model(&model.StorageContainer{}).
				Where("id = ? AND active = ? AND status = ? AND occupied + ? <= capacity",
					containerID, true, "available", added).
				UpdateColumn("occupied", gorm.Expr("occupied + ?", added))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrTargetContainerFull
			}
		}
		if err := tx.Model(&model.StorageContainer{}).Where("id = ?", source.ID).
			UpdateColumn("occupied", gorm.Expr("GREATEST(occupied - ?, 0)", len(plan.Items))).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.Find(ctx, incident.ID)
}

func (r *incidentRepository) Resolve(ctx context.Context, incidentID uint, resolution IncidentResolution) (*model.TemperatureIncident, error) {
	var incident model.TemperatureIncident
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&incident, incidentID).Error; err != nil {
			return err
		}
		if incident.State != constants.IncidentStateOpen {
			return ErrIncidentNotOpen
		}
		endTemp := resolution.EndTempC
		incident.State = constants.IncidentStateResolved
		incident.EndTempC = &endTemp
		incident.Conclusion = strings.TrimSpace(resolution.Conclusion)
		incident.RecoveredByID = &resolution.ResolvedByID
		incident.RecoveredByName = strings.TrimSpace(resolution.ResolvedByName)
		incident.ResolvedAt = &resolution.ResolvedAt
		incident.Normalize()
		if err := incident.Validate(); err != nil {
			return err
		}
		if err := tx.Save(&incident).Error; err != nil {
			return err
		}
		// A recovered freezer leaves the alarm state; manually maintained
		// containers keep their maintenance status.
		return tx.Model(&model.StorageContainer{}).
			Where("id = ? AND status = ?", incident.ContainerID, "alarm").
			UpdateColumn("status", "available").Error
	})
	if err != nil {
		return nil, err
	}
	return r.Find(ctx, incident.ID)
}

func (r *incidentRepository) CountOpenForSpecimen(ctx context.Context, specimenID uint) (int64, error) {
	return r.countOpenForSpecimen(r.db.WithContext(ctx), specimenID)
}

func (r *incidentRepository) CountOpenForContainer(ctx context.Context, containerID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.TemperatureIncident{}).
		Where("container_id = ? AND state = ?", containerID, constants.IncidentStateOpen).
		Count(&count).Error
	return count, err
}

func (r *incidentRepository) CountOpenForSpecimenTx(tx *gorm.DB, specimenID uint) (int64, error) {
	return r.countOpenForSpecimen(tx, specimenID)
}

func (r *incidentRepository) CountOpenForContainerTx(tx *gorm.DB, containerID uint) (int64, error) {
	var count int64
	err := tx.Model(&model.TemperatureIncident{}).
		Where("container_id = ? AND state = ?", containerID, constants.IncidentStateOpen).
		Count(&count).Error
	return count, err
}

func (r *incidentRepository) countOpenForSpecimen(db *gorm.DB, specimenID uint) (int64, error) {
	var count int64
	err := db.Model(&model.TemperatureIncidentItem{}).
		Joins("JOIN temperature_incidents ON temperature_incidents.id = temperature_incident_items.incident_id").
		Where("temperature_incident_items.specimen_id = ? AND temperature_incidents.state = ?",
			specimenID, constants.IncidentStateOpen).
		Count(&count).Error
	return count, err
}

func derefUint(value *uint) uint {
	if value == nil {
		return 0
	}
	return *value
}
