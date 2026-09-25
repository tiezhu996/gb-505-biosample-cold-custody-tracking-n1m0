// Package testsupport 提供需要真实 PostgreSQL 的集成测试辅助能力。
// 仅在设置 IT_DATABASE_URL 时启用；每个测试使用独立数据库，避免包级并行竞争。
package testsupport

import (
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"biosample-cold-custody-tracking/backend/internal/model"
)

var databaseCounter int64

// NewIntegrationDB 基于 IT_DATABASE_URL 创建一个带随机后缀的独立数据库并完成迁移。
// 返回数据库连接及其 DSN。
func NewIntegrationDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("IT_DATABASE_URL")
	if dsn == "" {
		t.Skip("IT_DATABASE_URL not set")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open root db: %v", err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	sequence := atomic.AddInt64(&databaseCounter, 1)
	name := fmt.Sprintf("it_%d_%d_%d", time.Now().UnixNano(), sequence, time.Now().Unix()%100000)
	if err := root.Exec("CREATE DATABASE " + name + " ENCODING 'UTF8' TEMPLATE template0").Error; err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = root.Exec("DROP DATABASE IF EXISTS " + name).Error
	})
	parsed.Path = "/" + name
	integratedDSN := parsed.String()
	db, err := gorm.Open(postgres.Open(integratedDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open integration db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.StorageContainer{}, &model.Specimen{},
		&model.CustodyTransfer{}, &model.ProtocolReview{},
		&model.TemperatureException{}, &model.TemperatureExceptionItem{}, &model.AuditLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, integratedDSN
}
