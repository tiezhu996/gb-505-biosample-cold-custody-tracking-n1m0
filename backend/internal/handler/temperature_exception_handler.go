package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/service"
	"biosample-cold-custody-tracking/backend/internal/util"
)

type TemperatureExceptionHandler struct {
	service service.TemperatureExceptionService
}

func NewTemperatureExceptionHandler(exceptionService service.TemperatureExceptionService) *TemperatureExceptionHandler {
	return &TemperatureExceptionHandler{service: exceptionService}
}

func (h *TemperatureExceptionHandler) List(c *gin.Context) {
	var filter repository.TemperatureExceptionFilter
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

func (h *TemperatureExceptionHandler) Get(c *gin.Context) {
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

func (h *TemperatureExceptionHandler) Open(c *gin.Context) {
	var input dto.CreateTemperatureExceptionRequest
	if !util.BindJSON(c, &input) {
		return
	}
	item, err := h.service.Open(c.Request.Context(), ActorFromContext(c), input)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusCreated, item)
}

func (h *TemperatureExceptionHandler) Close(c *gin.Context) {
	id, ok := util.ParseID(c)
	if !ok {
		return
	}
	var input dto.CloseTemperatureExceptionRequest
	if !util.BindJSON(c, &input) {
		return
	}
	item, err := h.service.Close(c.Request.Context(), ActorFromContext(c), id, input)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusOK, item)
}
