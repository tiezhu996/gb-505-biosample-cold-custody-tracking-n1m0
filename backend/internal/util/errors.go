package util

import (
	"errors"

	"gorm.io/gorm"
)

// MapDomainError converts database-level "not found" errors into the shared
// API representation; other errors pass through unchanged.
func MapDomainError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NotFound("记录不存在")
	}
	return err
}
