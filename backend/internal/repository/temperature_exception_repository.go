package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
)

var (
	ErrExceptionAlreadyOpen    = errors.New("container already has an open temperature exception")
	ErrExceptionNotOpen        = errors.New("temperature exception is already closed")
	ErrExceptionItemMismatch   = errors.New("relocation items must cover every specimen stored in the container")
	ErrExceptionTargetInvalid  = errors.New("relocation target container is unavailable, full, in another zone, or has an open exception")
	ErrExceptionTargetPosition = errors.New("relocation target position is occupied or duplicated in the order")
	ErrSpecimenUnderException  = errors.New("specimen is under an open temperature exception")
	ErrTargetUnderException    = errors.New("target container has an open temperature exception")
	ErrSpecimenTransferPending = errors.New("specimen has a pending custody transfer and cannot be relocated")
)

type TemperatureExceptionFilter struct {
	dto.PageQuery
	State       string `form:"state"`
	ContainerID uint   `form:"containerId"`
	SpecimenID  uint   `form:"specimenId"`
}

type TemperatureExceptionOpening struct {
	Exception *model.TemperatureException
	Items     []model.TemperatureExceptionItem
}

type TemperatureExceptionClosure struct {
	EndTemperatureC float64
	Outcome         constants.TemperatureExceptionOutcome
	ConclusionNotes string
	ClosedByName    string
	ClosedAt        time.Time
}

type TemperatureExceptionRepository interface {
	List(context.Context, TemperatureExceptionFilter) ([]model.TemperatureException, int64, error)
	Find(context.Context, uint) (*model.TemperatureException, error)
	FindByNumber(context.Context, string) (*model.TemperatureException, error)
	Open(context.Context, TemperatureExceptionOpening) (*model.TemperatureException, []model.Specimen, []model.Specimen, error)
	Close(context.Context, uint, TemperatureExceptionClosure) (*model.TemperatureException, model.StorageContainer, model.StorageContainer, error)
	CountOpenForSpecimen(context.Context, uint) (int64, error)
	CountOpenForContainer(context.Context, uint) (int64, error)
}

type temperatureExceptionRepository struct{ db *gorm.DB }

func NewTemperatureExceptionRepository(db *gorm.DB) TemperatureExceptionRepository {
	return &temperatureExceptionRepository{db: db}
}

// CountOpenForSpecimenTx reports whether a specimen is blocked by an open
// exception order: it is either listed on the order (including specimens
// already moved to a target container), or it is still stored in an
// alarming container with an open order.
func CountOpenForSpecimenTx(tx *gorm.DB, specimenID uint) (int64, error) {
	var count int64
	err := tx.Model(&model.TemperatureExceptionItem{}).
		Joins("JOIN temperature_exceptions ON temperature_exceptions.id = temperature_exception_items.exception_id").
		Where("temperature_exception_items.specimen_id = ? AND temperature_exceptions.state = ?", specimenID, constants.TemperatureExceptionOpen).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	if count > 0 {
		return count, nil
	}
	err = tx.Model(&model.Specimen{}).
		Joins("JOIN temperature_exceptions ON temperature_exceptions.container_id = specimens.storage_container_id").
		Where("specimens.id = ? AND specimens.state = ? AND temperature_exceptions.state = ?",
			specimenID, constants.SpecimenStateStored, constants.TemperatureExceptionOpen).
		Count(&count).Error
	return count, err
}

// CountOpenForContainerTx reports whether a container has an open exception.
func CountOpenForContainerTx(tx *gorm.DB, containerID uint) (int64, error) {
	var count int64
	err := tx.Model(&model.TemperatureException{}).
		Where("container_id = ? AND state = ?", containerID, constants.TemperatureExceptionOpen).
		Count(&count).Error
	return count, err
}

func (r *temperatureExceptionRepository) CountOpenForSpecimen(ctx context.Context, specimenID uint) (int64, error) {
	return CountOpenForSpecimenTx(r.db.WithContext(ctx), specimenID)
}

func (r *temperatureExceptionRepository) CountOpenForContainer(ctx context.Context, containerID uint) (int64, error) {
	return CountOpenForContainerTx(r.db.WithContext(ctx), containerID)
}

func (r *temperatureExceptionRepository) List(ctx context.Context, filter TemperatureExceptionFilter) ([]model.TemperatureException, int64, error) {
	query := filter.PageQuery.Normalize()
	db := r.db.WithContext(ctx).Model(&model.TemperatureException{})
	if state := strings.TrimSpace(filter.State); state != "" {
		db = db.Where("temperature_exceptions.state = ?", state)
	}
	if filter.ContainerID > 0 {
		db = db.Where("temperature_exceptions.container_id = ?", filter.ContainerID)
	}
	if filter.SpecimenID > 0 {
		db = db.Where("temperature_exceptions.id IN (?)",
			r.db.WithContext(ctx).Model(&model.TemperatureExceptionItem{}).
				Select("exception_id").Where("specimen_id = ?", filter.SpecimenID))
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		like := "%" + search + "%"
		db = db.Where("exception_no ILIKE ? OR alarm_reason ILIKE ? OR handler_name ILIKE ?", like, like, like)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.TemperatureException, 0)
	err := db.Preload("Container").Preload("Items").Preload("Items.Specimen").
		Preload("Items.TargetContainer").
		Order("started_at DESC, id DESC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&items).Error
	return items, total, err
}

func (r *temperatureExceptionRepository) Find(ctx context.Context, id uint) (*model.TemperatureException, error) {
	var item model.TemperatureException
	err := r.db.WithContext(ctx).
		Preload("Container").
		Preload("Items").Preload("Items.Specimen").Preload("Items.Specimen.StorageContainer").
		Preload("Items.TargetContainer").
		First(&item, id).Error
	return &item, err
}

func (r *temperatureExceptionRepository) FindByNumber(ctx context.Context, number string) (*model.TemperatureException, error) {
	var item model.TemperatureException
	err := r.db.WithContext(ctx).Where("exception_no = ?", number).First(&item).Error
	return &item, err
}

func (r *temperatureExceptionRepository) Open(ctx context.Context, opening TemperatureExceptionOpening) (*model.TemperatureException, []model.Specimen, []model.Specimen, error) {
	exception := opening.Exception
	inputItems := opening.Items
	movedSpecimens := make([]model.Specimen, 0)
	beforeSpecimens := make([]model.Specimen, 0)

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.StorageContainer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&source, exception.ContainerID).Error; err != nil {
			return err
		}
		if !source.Active {
			return ErrExceptionTargetInvalid
		}
		openCount, err := CountOpenForContainerTx(tx, source.ID)
		if err != nil {
			return err
		}
		if openCount > 0 {
			return ErrExceptionAlreadyOpen
		}
		if source.AcceptsTemperature(exception.StartTemperatureC) {
			return fmt.Errorf("start temperature %.2f is still inside the container temperature zone", exception.StartTemperatureC)
		}

		var currentSpecimens []model.Specimen
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("storage_container_id = ? AND state = ?", source.ID, constants.SpecimenStateStored).
			Find(&currentSpecimens).Error; err != nil {
			return err
		}
		// 转柜必须逐支覆盖容器内全部样本；现场观察允许只登记重点样本，
		// 但容器整体进入告警后，容器下所有样本仍会被阻断交接与放行。
		if exception.Action == constants.TemperatureActionRelocation && len(inputItems) != len(currentSpecimens) {
			return ErrExceptionItemMismatch
		}
		if exception.Action == constants.TemperatureActionOnsite && len(inputItems) > len(currentSpecimens) {
			return ErrExceptionItemMismatch
		}
		currentByID := make(map[uint]model.Specimen, len(currentSpecimens))
		for _, specimen := range currentSpecimens {
			currentByID[specimen.ID] = specimen
		}

		reservedPositions := make(map[uint]map[string]struct{})
		items := make([]model.TemperatureExceptionItem, 0, len(inputItems))
		targets := make(map[uint]*model.StorageContainer)
		targetInbound := make(map[uint]int)
		for _, input := range inputItems {
			specimen, ok := currentByID[input.SpecimenID]
			if !ok {
				return ErrExceptionItemMismatch
			}
			delete(currentByID, input.SpecimenID)
			item := model.TemperatureExceptionItem{
				SpecimenID:            specimen.ID,
				SourcePosition:        specimen.Position,
				SourceTemperatureZone: source.TemperatureZone,
				Notes:                 strings.TrimSpace(input.Notes),
			}
			if exception.Action == constants.TemperatureActionRelocation {
				prepared, err := countPreparedTransfersTx(tx, specimen.ID)
				if err != nil {
					return err
				}
				if prepared > 0 {
					return ErrSpecimenTransferPending
				}
				if input.TargetContainerID == nil || *input.TargetContainerID == 0 || strings.TrimSpace(input.TargetPosition) == "" {
					return fmt.Errorf("specimen %s requires an explicit target container and position", specimen.AccessionNo)
				}
				if *input.TargetContainerID == source.ID {
					return fmt.Errorf("specimen %s cannot be relocated inside the alarming container", specimen.AccessionNo)
				}
				target, ok := targets[*input.TargetContainerID]
				if !ok {
					var locked model.StorageContainer
					if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, *input.TargetContainerID).Error; err != nil {
						return err
					}
					target = &locked
					targets[locked.ID] = target
				}
				if !target.Active || target.Status != "available" || target.TemperatureZone != source.TemperatureZone {
					return ErrExceptionTargetInvalid
				}
				targetOpen, err := CountOpenForContainerTx(tx, target.ID)
				if err != nil {
					return err
				}
				if targetOpen > 0 {
					return ErrTargetUnderException
				}
				position := strings.TrimSpace(input.TargetPosition)
				if reservedPositions[target.ID] == nil {
					reservedPositions[target.ID] = make(map[string]struct{})
				}
				if _, duplicate := reservedPositions[target.ID][position]; duplicate {
					return ErrExceptionTargetPosition
				}
				var occupied int64
				if err := tx.Model(&model.Specimen{}).
					Where("storage_container_id = ? AND position = ? AND state NOT IN ?",
						target.ID, position, []constants.SpecimenState{constants.SpecimenStateReleased, constants.SpecimenStateDisposed}).
					Count(&occupied).Error; err != nil {
					return err
				}
				if occupied > 0 {
					return ErrExceptionTargetPosition
				}
				reservedPositions[target.ID][position] = struct{}{}
				targetInbound[target.ID]++
				item.TargetContainerID = &target.ID
				item.TargetPosition = position
				item.Moved = true
			}
			if err := item.Validate(); err != nil {
				return fmt.Errorf("validate exception item: %w", err)
			}
			items = append(items, item)
		}
		if exception.Action == constants.TemperatureActionRelocation && len(currentByID) > 0 {
			return ErrExceptionItemMismatch
		}

		exception.State = constants.TemperatureExceptionOpen
		exception.Normalize()
		if err := exception.Validate(); err != nil {
			return fmt.Errorf("validate temperature exception: %w", err)
		}
		if err := tx.Create(exception).Error; err != nil {
			return err
		}
		for index := range items {
			items[index].ExceptionID = exception.ID
			if err := tx.Create(&items[index]).Error; err != nil {
				return err
			}
		}

		if exception.Action == constants.TemperatureActionRelocation {
			for containerID := range targetInbound {
				// 行锁持有的目标柜计数 + 本单拟入数必须不超过容量。
				target := targets[containerID]
				if target.Occupied+targetInbound[containerID] > target.Capacity {
					return ErrExceptionTargetInvalid
				}
			}
			for containerID, amount := range targetInbound {
				result := tx.Model(&model.StorageContainer{}).
					Where("id = ? AND active = ? AND status = ? AND occupied + ? <= capacity",
						containerID, true, "available", amount).
					UpdateColumn("occupied", gorm.Expr("occupied + ?", amount))
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrExceptionTargetInvalid
				}
			}
			if len(items) > 0 {
				if err := tx.Model(&model.StorageContainer{}).Where("id = ?", source.ID).
					UpdateColumn("occupied", gorm.Expr("GREATEST(occupied - ?, 0)", len(items))).Error; err != nil {
					return err
				}
			}
			for _, item := range items {
				var specimen model.Specimen
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&specimen, item.SpecimenID).Error; err != nil {
					return err
				}
				beforeSpecimens = append(beforeSpecimens, specimen)
				if item.Moved {
					specimen.StorageContainerID = item.TargetContainerID
					specimen.Position = item.TargetPosition
				}
				if err := specimen.Validate(); err != nil {
					return fmt.Errorf("validate relocated specimen: %w", err)
				}
				if err := tx.Save(&specimen).Error; err != nil {
					return err
				}
				movedSpecimens = append(movedSpecimens, specimen)
			}
		}

		if err := tx.Model(&model.StorageContainer{}).Where("id = ?", source.ID).
			UpdateColumn("status", "alarm").Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	created, err := r.Find(ctx, exception.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	return created, movedSpecimens, beforeSpecimens, nil
}

func (r *temperatureExceptionRepository) Close(ctx context.Context, id uint, closure TemperatureExceptionClosure) (*model.TemperatureException, model.StorageContainer, model.StorageContainer, error) {
	var before, after model.StorageContainer
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var exception model.TemperatureException
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&exception, id).Error; err != nil {
			return err
		}
		if exception.State != constants.TemperatureExceptionOpen {
			return ErrExceptionNotOpen
		}
		var source model.StorageContainer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&source, exception.ContainerID).Error; err != nil {
			return err
		}
		before = source
		if closure.Outcome == constants.TemperatureOutcomeRecovered && !source.AcceptsTemperature(closure.EndTemperatureC) {
			return fmt.Errorf("end temperature %.2f is still outside the %s temperature zone", closure.EndTemperatureC, source.TemperatureZone)
		}
		end := closure.EndTemperatureC
		now := closure.ClosedAt
		exception.State = constants.TemperatureExceptionClosed
		exception.EndTemperatureC = &end
		exception.Outcome = closure.Outcome
		exception.ConclusionNotes = strings.TrimSpace(closure.ConclusionNotes)
		exception.ClosedByName = strings.TrimSpace(closure.ClosedByName)
		exception.ClosedAt = &now
		exception.Normalize()
		if err := exception.Validate(); err != nil {
			return fmt.Errorf("validate closed exception: %w", err)
		}
		if err := tx.Save(&exception).Error; err != nil {
			return err
		}
		nextStatus := "available"
		if closure.Outcome == constants.TemperatureOutcomeDiscarded {
			nextStatus = "maintenance"
		}
		if err := tx.Model(&model.StorageContainer{}).Where("id = ?", source.ID).
			UpdateColumn("status", nextStatus).Error; err != nil {
			return err
		}
		source.Status = nextStatus
		after = source
		return nil
	})
	if err != nil {
		return nil, model.StorageContainer{}, model.StorageContainer{}, err
	}
	closed, err := r.Find(ctx, id)
	if err != nil {
		return nil, model.StorageContainer{}, model.StorageContainer{}, err
	}
	return closed, after, before, nil
}

func countPreparedTransfersTx(tx *gorm.DB, specimenID uint) (int64, error) {
	var count int64
	err := tx.Model(&model.CustodyTransfer{}).
		Where("specimen_id = ? AND state = ?", specimenID, constants.TransferStatePrepared).
		Count(&count).Error
	return count, err
}
