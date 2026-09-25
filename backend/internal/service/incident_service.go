package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/util"
)

type IncidentService interface {
	List(context.Context, repository.IncidentFilter) (dto.PageResult[model.TemperatureIncident], error)
	Get(context.Context, uint) (*model.TemperatureIncident, error)
	Create(context.Context, Actor, dto.CreateIncidentRequest) (*model.TemperatureIncident, error)
	Relocate(context.Context, Actor, uint, dto.RelocateIncidentRequest) (*model.TemperatureIncident, error)
	Resolve(context.Context, Actor, uint, dto.ResolveIncidentRequest) (*model.TemperatureIncident, error)
}

type incidentService struct {
	repo          repository.IncidentRepository
	containerRepo repository.StorageRepository
	audit         AuditService
}

func NewIncidentService(repo repository.IncidentRepository, containerRepo repository.StorageRepository, audit AuditService) IncidentService {
	return &incidentService{repo: repo, containerRepo: containerRepo, audit: audit}
}

func (s *incidentService) List(ctx context.Context, filter repository.IncidentFilter) (dto.PageResult[model.TemperatureIncident], error) {
	query := filter.PageQuery.Normalize()
	filter.PageQuery = query
	items, total, err := s.repo.List(ctx, filter)
	return dto.PageResult[model.TemperatureIncident]{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize}, err
}

func (s *incidentService) Get(ctx context.Context, id uint) (*model.TemperatureIncident, error) {
	return s.repo.Find(ctx, id)
}

func (s *incidentService) Create(ctx context.Context, actor Actor, input dto.CreateIncidentRequest) (*model.TemperatureIncident, error) {
	container, err := s.containerRepo.Find(ctx, input.ContainerID)
	if err != nil {
		return nil, err
	}
	if !container.Active {
		return nil, util.Conflict("容器已停用，不能登记温度异常处置单")
	}
	if container.AcceptsTemperature(*input.StartTempC) {
		return nil, util.BadRequest("登记温度仍处于容器正常温区内，不构成温度异常")
	}
	startedAt := time.Now().UTC()
	if input.StartedAt != nil {
		startedAt = input.StartedAt.UTC()
	}
	if startedAt.After(time.Now().Add(5 * time.Minute)) {
		return nil, util.BadRequest("报警起始时间不能晚于当前时间")
	}
	number, err := s.generateNumber(ctx, startedAt)
	if err != nil {
		return nil, err
	}
	incident := &model.TemperatureIncident{
		IncidentNo:  number,
		ContainerID: container.ID,
		State:       constants.IncidentStateOpen,
		StartTempC:  *input.StartTempC,
		AlarmReason: input.AlarmReason,
		HandlerID:   actor.ID,
		HandlerName: actor.Name,
		StartedAt:   startedAt,
	}
	incident.Normalize()
	if err := incident.Validate(); err != nil {
		return nil, util.BadRequest(err.Error())
	}
	if err := s.repo.Create(ctx, incident); err != nil {
		return nil, s.mapRepositoryError(err)
	}
	created, err := s.repo.Find(ctx, incident.ID)
	if err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "temperature_incident.opened", "TemperatureIncident", incident.ID, nil, created); err != nil {
		return nil, err
	}
	return s.repo.Find(ctx, incident.ID)
}

func (s *incidentService) Relocate(ctx context.Context, actor Actor, id uint, input dto.RelocateIncidentRequest) (*model.TemperatureIncident, error) {
	incident, err := s.repo.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if incident.State != constants.IncidentStateOpen {
		return nil, util.Conflict("处置单已结案，不能继续转柜")
	}
	covered := make(map[uint]struct{}, len(incident.Items))
	for _, item := range incident.Items {
		covered[item.SpecimenID] = struct{}{}
	}
	seen := make(map[uint]struct{}, len(input.Items))
	plan := repository.IncidentRelocationPlan{Items: make([]repository.IncidentRelocationPlanItem, 0, len(input.Items))}
	movedAt := time.Now().UTC()
	for _, requested := range input.Items {
		if _, repeated := seen[requested.SpecimenID]; repeated {
			return nil, util.BadRequest("同一支样本在本次转柜中只能指定一次")
		}
		seen[requested.SpecimenID] = struct{}{}
		if _, ok := covered[requested.SpecimenID]; !ok {
			return nil, util.Conflict(fmt.Sprintf("样本 #%d 不在处置单 %s 管控范围内", requested.SpecimenID, incident.IncidentNo))
		}
		if requested.TargetContainerID == incident.ContainerID {
			return nil, util.BadRequest("转柜目标容器不能与报警容器相同")
		}
		target, err := s.containerRepo.Find(ctx, requested.TargetContainerID)
		if err != nil {
			return nil, err
		}
		if !model.CanRelocateZone(incidentContainerZone(incident), target.TemperatureZone) {
			return nil, util.Conflict(fmt.Sprintf("目标容器 %s 温区不能高于报警容器温区", target.Code))
		}
		if !target.Active || target.Status != "available" {
			return nil, util.Conflict(fmt.Sprintf("目标容器 %s 当前不可接收样本", target.Code))
		}
		openCount, err := s.repo.CountOpenForContainer(ctx, target.ID)
		if err != nil {
			return nil, err
		}
		if openCount > 0 {
			return nil, util.Conflict(fmt.Sprintf("目标容器 %s 存在未结案温度异常处置单", target.Code))
		}
		plan.Items = append(plan.Items, repository.IncidentRelocationPlanItem{
			SpecimenID:        requested.SpecimenID,
			TargetContainerID: requested.TargetContainerID,
			TargetPosition:    strings.TrimSpace(requested.TargetPosition),
			OperatorID:        actor.ID,
			OperatorName:      actor.Name,
			MovedAt:           movedAt,
		})
	}
	relocated, err := s.repo.Relocate(ctx, id, plan)
	if err != nil {
		return nil, s.mapRepositoryError(err)
	}
	if err := s.audit.Record(ctx, actor, "temperature_incident.relocated", "TemperatureIncident", id, incident, relocated); err != nil {
		return nil, err
	}
	return s.repo.Find(ctx, id)
}

func (s *incidentService) Resolve(ctx context.Context, actor Actor, id uint, input dto.ResolveIncidentRequest) (*model.TemperatureIncident, error) {
	before, err := s.repo.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if before.State != constants.IncidentStateOpen {
		return nil, util.Conflict("处置单已结案，不能重复登记恢复")
	}
	if before.Container != nil && !before.Container.AcceptsTemperature(*input.EndTempC) {
		return nil, util.BadRequest("结束温度仍超出容器正常温区，不能确认恢复；请继续处置后再登记")
	}
	resolvedAt := time.Now().UTC()
	if input.ResolvedAt != nil {
		resolvedAt = input.ResolvedAt.UTC()
	}
	if resolvedAt.Before(before.StartedAt) {
		return nil, util.BadRequest("恢复时间不能早于报警起始时间")
	}
	resolution := repository.IncidentResolution{
		EndTempC:       *input.EndTempC,
		Conclusion:     input.Conclusion,
		ResolvedByID:   actor.ID,
		ResolvedByName: actor.Name,
		ResolvedAt:     resolvedAt,
	}
	resolved, err := s.repo.Resolve(ctx, id, resolution)
	if err != nil {
		return nil, s.mapRepositoryError(err)
	}
	if err := s.audit.Record(ctx, actor, "temperature_incident.resolved", "TemperatureIncident", id, before, resolved); err != nil {
		return nil, err
	}
	return s.repo.Find(ctx, id)
}

func (s *incidentService) generateNumber(ctx context.Context, startedAt time.Time) (string, error) {
	prefix := fmt.Sprintf("TI-%s-", startedAt.Format("20060102"))
	dayStart := time.Date(startedAt.Year(), startedAt.Month(), startedAt.Day(), 0, 0, 0, 0, time.UTC)
	count, err := s.repo.CountToday(ctx, dayStart)
	if err != nil {
		return "", err
	}
	for attempt := int64(0); attempt < 20; attempt++ {
		number := fmt.Sprintf("%s%03d", prefix, count+1+attempt)
		if _, err := s.repo.FindByNumber(ctx, number); errors.Is(err, gorm.ErrRecordNotFound) {
			return number, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", util.Conflict("处置单编号生成冲突，请稍后重试")
}

func incidentContainerZone(incident *model.TemperatureIncident) string {
	if incident.Container != nil {
		return incident.Container.TemperatureZone
	}
	return ""
}

func (s *incidentService) mapRepositoryError(err error) error {
	switch {
	case errors.Is(err, repository.ErrIncidentAlreadyOpen):
		return util.Conflict("该容器已存在未结案的温度异常处置单")
	case errors.Is(err, repository.ErrIncidentNotOpen):
		return util.Conflict("处置单已经结案")
	case errors.Is(err, repository.ErrIncidentItemMismatch):
		return util.BadRequest("转柜样本不属于该处置单或存在重复登记")
	case errors.Is(err, repository.ErrIncidentSpecimenMoved):
		return util.Conflict("样本已不在报警容器中，处置单需要重新核对")
	case errors.Is(err, repository.ErrIncidentAlreadyRelocated):
		return util.Conflict("存在已完成转柜的样本，整单未生效")
	case errors.Is(err, repository.ErrIncidentTargetSame):
		return util.BadRequest("转柜目标容器不能与报警容器相同")
	case errors.Is(err, repository.ErrIncidentZoneUnfit):
		return util.Conflict("目标容器温区高于报警容器，不能保证样本安全")
	case errors.Is(err, repository.ErrSpecimenIncidentOpen):
		return util.Conflict("样本受未结案温度异常处置单管控")
	case errors.Is(err, repository.ErrContainerIncidentOpen):
		return util.Conflict("目标容器存在未结案温度异常处置单")
	case errors.Is(err, repository.ErrTargetContainerFull):
		return util.Conflict("目标容器不可用或容量不足，整单未生效")
	case errors.Is(err, repository.ErrPositionOccupied):
		return util.Conflict("目标格位已被占用或与单内其他样本重复，整单未生效")
	default:
		return err
	}
}
