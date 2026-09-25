package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/util"
)

type TemperatureExceptionService interface {
	List(context.Context, repository.TemperatureExceptionFilter) (dto.PageResult[model.TemperatureException], error)
	Get(context.Context, uint) (*model.TemperatureException, error)
	Open(context.Context, Actor, dto.CreateTemperatureExceptionRequest) (*model.TemperatureException, error)
	Close(context.Context, Actor, uint, dto.CloseTemperatureExceptionRequest) (*model.TemperatureException, error)
}

type temperatureExceptionService struct {
	repo        repository.TemperatureExceptionRepository
	storageRepo repository.StorageRepository
	audit       AuditService
}

func NewTemperatureExceptionService(repo repository.TemperatureExceptionRepository, storageRepo repository.StorageRepository, audit AuditService) TemperatureExceptionService {
	return &temperatureExceptionService{repo: repo, storageRepo: storageRepo, audit: audit}
}

func (s *temperatureExceptionService) List(ctx context.Context, filter repository.TemperatureExceptionFilter) (dto.PageResult[model.TemperatureException], error) {
	query := filter.PageQuery.Normalize()
	filter.PageQuery = query
	items, total, err := s.repo.List(ctx, filter)
	return dto.PageResult[model.TemperatureException]{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize}, err
}

func (s *temperatureExceptionService) Get(ctx context.Context, id uint) (*model.TemperatureException, error) {
	return s.repo.Find(ctx, id)
}

func (s *temperatureExceptionService) Open(ctx context.Context, actor Actor, input dto.CreateTemperatureExceptionRequest) (*model.TemperatureException, error) {
	number := strings.ToUpper(strings.TrimSpace(input.ExceptionNo))
	if _, err := s.repo.FindByNumber(ctx, number); err == nil {
		return nil, util.Conflict("温度异常处置单号已存在")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	container, err := s.storageRepo.Find(ctx, input.ContainerID)
	if err != nil {
		return nil, err
	}
	if container.AcceptsTemperature(input.StartTemperatureC) {
		return nil, util.BadRequest("起始温度仍处于容器正常温区内，不能按温度异常登记")
	}
	handler := strings.TrimSpace(input.HandlerName)
	if handler == "" {
		handler = strings.TrimSpace(actor.Name)
	}
	items := make([]model.TemperatureExceptionItem, 0, len(input.Items))
	seen := make(map[uint]struct{}, len(input.Items))
	for _, item := range input.Items {
		if item.SpecimenID == 0 {
			return nil, util.BadRequest("转柜明细必须指定样本")
		}
		if _, duplicate := seen[item.SpecimenID]; duplicate {
			return nil, util.BadRequest("同一支样本在处置单中只能出现一次")
		}
		seen[item.SpecimenID] = struct{}{}
		position := strings.TrimSpace(item.TargetPosition)
		items = append(items, model.TemperatureExceptionItem{
			SpecimenID:        item.SpecimenID,
			TargetContainerID: item.TargetContainerID,
			TargetPosition:    position,
			Notes:             strings.TrimSpace(item.Notes),
		})
	}
	exception := &model.TemperatureException{
		ExceptionNo:       number,
		ContainerID:       container.ID,
		StartTemperatureC: input.StartTemperatureC,
		AlarmReason:       input.AlarmReason,
		HandlerName:       handler,
		Action:            input.Action,
		State:             "open",
		StartedAt:         time.Now().UTC(),
	}
	exception.Normalize()
	if err := exception.Validate(); err != nil {
		return nil, util.BadRequest(err.Error())
	}
	opened, movedSpecimens, beforeSpecimens, err := s.repo.Open(ctx, repository.TemperatureExceptionOpening{
		Exception: exception,
		Items:     items,
	})
	if err != nil {
		return nil, mapTemperatureExceptionError(err)
	}
	if err := s.audit.Record(ctx, actor, "temperature_exception.opened", "TemperatureException", opened.ID, nil, opened); err != nil {
		return nil, err
	}
	containerAfter, err := s.storageRepo.Find(ctx, container.ID)
	if err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "storage_container.alarm", "StorageContainer", container.ID, container, containerAfter); err != nil {
		return nil, err
	}
	for index := range movedSpecimens {
		if err := s.audit.Record(ctx, actor, "specimen.relocated", "Specimen", movedSpecimens[index].ID, beforeSpecimens[index], movedSpecimens[index]); err != nil {
			return nil, err
		}
	}
	return s.repo.Find(ctx, opened.ID)
}

func (s *temperatureExceptionService) Close(ctx context.Context, actor Actor, id uint, input dto.CloseTemperatureExceptionRequest) (*model.TemperatureException, error) {
	before, err := s.repo.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if !before.Open() {
		return nil, util.Conflict("处置单已经结案")
	}
	handler := strings.TrimSpace(input.HandlerName)
	if handler == "" {
		handler = strings.TrimSpace(actor.Name)
	}
	closed, afterContainer, beforeContainer, err := s.repo.Close(ctx, id, repository.TemperatureExceptionClosure{
		EndTemperatureC: input.EndTemperatureC,
		Outcome:         input.Outcome,
		ConclusionNotes: input.ConclusionNotes,
		ClosedByName:    handler,
		ClosedAt:        time.Now().UTC(),
	})
	if err != nil {
		return nil, mapTemperatureExceptionError(err)
	}
	if err := s.audit.Record(ctx, actor, "temperature_exception.closed", "TemperatureException", id, before, closed); err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "storage_container.recovered", "StorageContainer", beforeContainer.ID, beforeContainer, afterContainer); err != nil {
		return nil, err
	}
	return s.repo.Find(ctx, id)
}

func mapTemperatureExceptionError(err error) error {
	switch {
	case errors.Is(err, repository.ErrExceptionAlreadyOpen):
		return util.Conflict("该容器已有未结案的温度异常处置单")
	case errors.Is(err, repository.ErrExceptionNotOpen):
		return util.Conflict("处置单已结案，不能重复结案")
	case errors.Is(err, repository.ErrExceptionItemMismatch):
		return util.Conflict("转柜明细必须逐支覆盖容器内全部在柜样本")
	case errors.Is(err, repository.ErrExceptionTargetInvalid):
		return util.Conflict("目标容器容量、温区或运行状态不合适，整单未生效")
	case errors.Is(err, repository.ErrExceptionTargetPosition):
		return util.Conflict("目标格位已占用或在本单中重复指定，整单未生效")
	case errors.Is(err, repository.ErrTargetUnderException):
		return util.Conflict("目标容器存在未结案温度异常，整单未生效")
	case errors.Is(err, repository.ErrSpecimenUnderException):
		return util.Conflict("样本处于温度异常期间，暂缓交接与放行")
	case errors.Is(err, repository.ErrSpecimenTransferPending):
		return util.Conflict("样本存在待处理交接，不能随处置单转柜，请先拒绝或取消该交接")
	default:
		return util.MapDomainError(err)
	}
}
