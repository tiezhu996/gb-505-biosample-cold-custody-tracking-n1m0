package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/service"
	"biosample-cold-custody-tracking/backend/internal/util"
)

type IncidentHandler struct{ service service.IncidentService }

func NewIncidentHandler(incidentService service.IncidentService) *IncidentHandler {
	return &IncidentHandler{service: incidentService}
}

func (h *IncidentHandler) List(c *gin.Context) {
	var filter repository.IncidentFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		util.RespondError(c, util.BadRequest(err.Error()))
		return
	}
	result, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusOK, result)
}

func (h *IncidentHandler) Get(c *gin.Context) {
	id, ok := util.ParseID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusOK, item)
}

func (h *IncidentHandler) Create(c *gin.Context) {
	var input dto.CreateIncidentRequest
	if !util.BindJSON(c, &input) {
		return
	}
	item, err := h.service.Create(c.Request.Context(), ActorFromContext(c), input)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusCreated, item)
}

func (h *IncidentHandler) Relocate(c *gin.Context) {
	id, ok := util.ParseID(c)
	if !ok {
		return
	}
	var input dto.RelocateIncidentRequest
	if !util.BindJSON(c, &input) {
		return
	}
	item, err := h.service.Relocate(c.Request.Context(), ActorFromContext(c), id, input)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusOK, item)
}

func (h *IncidentHandler) Resolve(c *gin.Context) {
	id, ok := util.ParseID(c)
	if !ok {
		return
	}
	var input dto.ResolveIncidentRequest
	if !util.BindJSON(c, &input) {
		return
	}
	item, err := h.service.Resolve(c.Request.Context(), ActorFromContext(c), id, input)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusOK, item)
}
