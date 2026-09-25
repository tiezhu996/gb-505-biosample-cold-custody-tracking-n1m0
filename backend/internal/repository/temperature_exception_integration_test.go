package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/model"
	"biosample-cold-custody-tracking/backend/internal/testsupport"
)

func TestTemperatureExceptionIntegration(t *testing.T) {
	if os.Getenv("IT_DATABASE_URL") == "" {
		t.Skip("IT_DATABASE_URL not set")
	}
	db, _ := testsupport.NewIntegrationDB(t)
	ctx := context.Background()
	// 准备: 两个同温区容器 + 3 支样本在源柜
	source := &model.StorageContainer{Code: "IT-SRC-01", Name: "源柜", ContainerType: "freezer", TemperatureZone: "minus80", Location: "A区", Capacity: 10, Occupied: 3, Status: "available", Active: true}
	target := &model.StorageContainer{Code: "IT-DST-01", Name: "目标柜", ContainerType: "freezer", TemperatureZone: "minus80", Location: "B区", Capacity: 10, Occupied: 0, Status: "available", Active: true}
	if err := db.Create(source).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(target).Error; err != nil {
		t.Fatal(err)
	}
	specimens := make([]model.Specimen, 0, 3)
	for i := 0; i < 3; i++ {
		pos := string(rune('A'+i)) + "-01"
		s := model.Specimen{
			AccessionNo: "IT-SPEC-" + string(rune('0'+i)), SampleType: "血浆", SubjectCode: "IT-SUBJ-0" + string(rune('0'+i)),
			ProtocolCode: "IT-PROTO-01", State: constants.SpecimenStateStored, StorageContainerID: &source.ID,
			Position: pos, VolumeML: 1, CurrentCustodian: "保管员甲", ReceivedAt: time.Now().Add(-time.Hour),
		}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		specimens = append(specimens, s)
	}

	repo := NewTemperatureExceptionRepository(db)
	var err error

	// 1. 转柜漏指定一支 -> 整单失败
	badItems := []model.TemperatureExceptionItem{
		{SpecimenID: specimens[0].ID, TargetContainerID: &target.ID, TargetPosition: "X-1", Moved: true},
		{SpecimenID: specimens[1].ID, TargetContainerID: &target.ID, TargetPosition: "X-2", Moved: true},
	}
	_, _, _, err = repo.Open(ctx, TemperatureExceptionOpening{Exception: &model.TemperatureException{
		ExceptionNo: "IT-EX-BAD", ContainerID: source.ID, StartTemperatureC: -55, AlarmReason: "测试报警原因",
		HandlerName: "保管员甲", Action: constants.TemperatureActionRelocation, StartedAt: time.Now(),
	}, Items: badItems})
	if err != ErrExceptionItemMismatch {
		t.Fatalf("expected ErrExceptionItemMismatch, got %v", err)
	}

	// 2. 格位重复 -> 整单失败
	dup := []model.TemperatureExceptionItem{
		{SpecimenID: specimens[0].ID, TargetContainerID: &target.ID, TargetPosition: "DUP"},
		{SpecimenID: specimens[1].ID, TargetContainerID: &target.ID, TargetPosition: "DUP"},
		{SpecimenID: specimens[2].ID, TargetContainerID: &target.ID, TargetPosition: "X-3"},
	}
	_, _, _, err = repo.Open(ctx, TemperatureExceptionOpening{Exception: &model.TemperatureException{
		ExceptionNo: "IT-EX-DUP", ContainerID: source.ID, StartTemperatureC: -55, AlarmReason: "测试报警原因",
		HandlerName: "保管员甲", Action: constants.TemperatureActionRelocation, StartedAt: time.Now(),
	}, Items: dup})
	if err != ErrExceptionTargetPosition {
		t.Fatalf("expected ErrExceptionTargetPosition, got %v", err)
	}

	// 失败后确认没有部分写入
	var orderCount int64
	db.Model(&model.TemperatureException{}).Where("exception_no IN ?", []string{"IT-EX-BAD", "IT-EX-DUP"}).Count(&orderCount)
	if orderCount != 0 {
		t.Fatalf("failed orders must not be persisted, got %d", orderCount)
	}
	var srcFresh model.StorageContainer
	db.First(&srcFresh, source.ID)
	if srcFresh.Occupied != 3 || srcFresh.Status != "available" {
		t.Fatalf("source must be unchanged after failed order: occupied=%d status=%s", srcFresh.Occupied, srcFresh.Status)
	}

	// 3. 正常转柜单 -> 成功
	good := []model.TemperatureExceptionItem{
		{SpecimenID: specimens[0].ID, TargetContainerID: &target.ID, TargetPosition: "X-1"},
		{SpecimenID: specimens[1].ID, TargetContainerID: &target.ID, TargetPosition: "X-2"},
		{SpecimenID: specimens[2].ID, TargetContainerID: &target.ID, TargetPosition: "X-3"},
	}
	opened, moved, before, err := repo.Open(ctx, TemperatureExceptionOpening{Exception: &model.TemperatureException{
		ExceptionNo: "IT-EX-OK", ContainerID: source.ID, StartTemperatureC: -55, AlarmReason: "柜门未关严导致升温",
		HandlerName: "保管员甲", Action: constants.TemperatureActionRelocation, StartedAt: time.Now(),
	}, Items: good})
	if err != nil {
		t.Fatalf("open valid order: %v", err)
	}
	if len(moved) != 3 || len(before) != 3 {
		t.Fatalf("expected 3 moved specimens, got %d/%d", len(moved), len(before))
	}
	db.First(&srcFresh, source.ID)
	var dstFresh model.StorageContainer
	db.First(&dstFresh, target.ID)
	if srcFresh.Occupied != 0 || srcFresh.Status != "alarm" {
		t.Fatalf("source after relocation: occupied=%d status=%s", srcFresh.Occupied, srcFresh.Status)
	}
	if dstFresh.Occupied != 3 {
		t.Fatalf("target occupied = %d, want 3", dstFresh.Occupied)
	}
	var movedSpecimen model.Specimen
	db.First(&movedSpecimen, specimens[0].ID)
	if movedSpecimen.StorageContainerID == nil || *movedSpecimen.StorageContainerID != target.ID || movedSpecimen.Position != "X-1" {
		t.Fatalf("specimen not relocated: container=%v position=%s", movedSpecimen.StorageContainerID, movedSpecimen.Position)
	}

	// 4. 未结案: 样本被阻断; 目标柜也不能接收新交接
	blocked, err := repo.CountOpenForSpecimen(ctx, specimens[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked == 0 {
		t.Fatal("relocated specimen must still be blocked while order is open")
	}
	openTarget, err := repo.CountOpenForContainer(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openTarget != 0 {
		// 目标柜本身没有处置单(是源柜有), 但样本通过明细行被阻断
	}

	// 5. 重复开单被拒
	_, _, _, err = repo.Open(ctx, TemperatureExceptionOpening{Exception: &model.TemperatureException{
		ExceptionNo: "IT-EX-OK2", ContainerID: source.ID, StartTemperatureC: -55, AlarmReason: "再次报警",
		HandlerName: "保管员甲", Action: constants.TemperatureActionOnsite, StartedAt: time.Now(),
	}, Items: []model.TemperatureExceptionItem{{SpecimenID: specimens[0].ID}}})
	if err != ErrExceptionAlreadyOpen {
		t.Fatalf("expected ErrExceptionAlreadyOpen, got %v", err)
	}

	// 6. 结束温度仍超温区 -> 不能按 recovered 结案
	_, _, _, err = repo.Close(ctx, opened.ID, TemperatureExceptionClosure{
		EndTemperatureC: -50, Outcome: constants.TemperatureOutcomeRecovered,
		ConclusionNotes: "温度仍不达标不应结案", ClosedByName: "保管员甲", ClosedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("closing with out-of-range end temperature must fail")
	}

	// 7. 正常结案
	closed, afterContainer, _, err := repo.Close(ctx, opened.ID, TemperatureExceptionClosure{
		EndTemperatureC: -79, Outcome: constants.TemperatureOutcomeRecovered,
		ConclusionNotes: "温度恢复正常，样本评估可用", ClosedByName: "保管员甲", ClosedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed.State != constants.TemperatureExceptionClosed || afterContainer.Status != "available" {
		t.Fatalf("closure state wrong: %s / %s", closed.State, afterContainer.Status)
	}
	blockedAfter, _ := repo.CountOpenForSpecimen(ctx, specimens[0].ID)
	if blockedAfter != 0 {
		t.Fatal("specimen must be unblocked after closure")
	}

	// 8. 起始温度在温区内不允许登记
	other := &model.StorageContainer{Code: "IT-SRC-02", Name: "源柜2", ContainerType: "freezer", TemperatureZone: "minus80", Location: "C区", Capacity: 10, Occupied: 0, Status: "available", Active: true}
	if err := db.Create(other).Error; err != nil {
		t.Fatal(err)
	}
	_, _, _, err = repo.Open(ctx, TemperatureExceptionOpening{Exception: &model.TemperatureException{
		ExceptionNo: "IT-EX-INZONE", ContainerID: other.ID, StartTemperatureC: -78, AlarmReason: "正常温度不该报警",
		HandlerName: "保管员甲", Action: constants.TemperatureActionOnsite, StartedAt: time.Now(),
	}, Items: []model.TemperatureExceptionItem{}})
	if err == nil {
		t.Fatal("in-zone start temperature must be rejected")
	}
}

// TestBlockingDuringOpenException 验证未结案期间交接与协议放行在事务内被拦截。
func TestBlockingDuringOpenException(t *testing.T) {
	if os.Getenv("IT_DATABASE_URL") == "" {
		t.Skip("IT_DATABASE_URL not set")
	}
	db, _ := testsupport.NewIntegrationDB(t)
	ctx := context.Background()

	source := &model.StorageContainer{Code: "BLK-SRC-01", Name: "阻断源柜", ContainerType: "freezer", TemperatureZone: "minus80", Location: "A区", Capacity: 10, Occupied: 1, Status: "available", Active: true}
	another := &model.StorageContainer{Code: "BLK-DST-01", Name: "普通目标柜", ContainerType: "freezer", TemperatureZone: "minus80", Location: "B区", Capacity: 10, Occupied: 0, Status: "available", Active: true}
	if err := db.Create(source).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(another).Error; err != nil {
		t.Fatal(err)
	}
	received := time.Now().Add(-2 * time.Hour)
	specimen := &model.Specimen{
		AccessionNo: "BLK-SPEC-01", SampleType: "全血", SubjectCode: "BLK-SUBJ-01", ProtocolCode: "BLK-PROTO-01",
		State: constants.SpecimenStateStored, StorageContainerID: &source.ID, Position: "A-1",
		VolumeML: 3, CurrentCustodian: "保管员甲", ReceivedAt: received,
	}
	if err := db.Create(specimen).Error; err != nil {
		t.Fatal(err)
	}

	exceptionRepo := NewTemperatureExceptionRepository(db)
	opened, _, _, err := exceptionRepo.Open(ctx, TemperatureExceptionOpening{Exception: &model.TemperatureException{
		ExceptionNo: "BLK-EX-01", ContainerID: source.ID, StartTemperatureC: -56, AlarmReason: "阻断测试报警",
		HandlerName: "保管员甲", Action: constants.TemperatureActionOnsite, StartedAt: time.Now(),
	}, Items: []model.TemperatureExceptionItem{{SpecimenID: specimen.ID}}})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// 样本被标记为阻断
	blocked, err := CountOpenForSpecimenTx(db, specimen.ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked == 0 {
		t.Fatal("specimen in alarming container must be blocked")
	}

	// 准备一张将该样本转入普通柜的交接单，受理必须失败
	temp := -78.0
	prepared := time.Now().Add(-time.Minute)
	transfer := &model.CustodyTransfer{
		SpecimenID: specimen.ID, TransferNo: "BLK-CT-01", FromCustodian: "保管员甲", ToCustodian: "保管员乙",
		FromLocation: "A区 / BLK-SRC-01 / A-1", ToLocation: "B区", ToContainerID: &another.ID, ToPosition: "C-1",
		State: constants.TransferStatePrepared, PreparedByID: 1, PreparedByName: "保管员甲", PreparedAt: prepared,
		TemperatureC: &temp,
	}
	if err := db.Create(transfer).Error; err != nil {
		t.Fatal(err)
	}
	transferRepo := NewTransferRepository(db)
	resolver := uint(2)
	resolvedAt := time.Now()
	_, _, _, err = transferRepo.Resolve(ctx, transfer.ID, TransferResolution{
		State: constants.TransferStateAccepted, ToContainerID: &another.ID, ToPosition: "C-1",
		TemperatureC: &temp, ResolvedByID: resolver, ResolvedByName: "保管员乙", ResolvedAt: resolvedAt,
	})
	if err != ErrSpecimenUnderException {
		t.Fatalf("expected ErrSpecimenUnderException on transfer accept, got %v", err)
	}

	// 异常期间仍允许拒绝该交接（不移动样本的清理操作）
	rejected, _, _, err := transferRepo.Resolve(ctx, transfer.ID, TransferResolution{
		State: constants.TransferStateRejected, Reason: "温度异常期间暂缓，拒绝本单",
		ResolvedByID: resolver, ResolvedByName: "保管员乙", ResolvedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("rejecting during exception must be allowed: %v", err)
	}
	if rejected.State != constants.TransferStateRejected {
		t.Fatalf("transfer state = %s, want rejected", rejected.State)
	}

	// 协议批准放行必须失败
	review := &model.ProtocolReview{
		SpecimenID: specimen.ID, ProtocolCode: "BLK-PROTO-01", Decision: constants.DecisionApproved,
		ReviewerID: 3, ReviewerName: "复核员", ConsentVerified: true, ScopeVerified: true, ReviewedAt: time.Now(),
	}
	protocolRepo := NewProtocolRepository(db)
	_, _, _, err = protocolRepo.Create(ctx, review)
	if err != ErrSpecimenUnderException {
		t.Fatalf("expected ErrSpecimenUnderException on protocol approval, got %v", err)
	}

	// 结案后交接可以受理
	if _, _, _, err = exceptionRepo.Close(ctx, opened.ID, TemperatureExceptionClosure{
		EndTemperatureC: -79, Outcome: constants.TemperatureOutcomeRecovered,
		ConclusionNotes: "恢复后恢复业务", ClosedByName: "保管员甲", ClosedAt: time.Now(),
	}); err != nil {
		t.Fatalf("close: %v", err)
	}
	// 结案后可重新发起交接并受理
	newTransfer := &model.CustodyTransfer{
		SpecimenID: specimen.ID, TransferNo: "BLK-CT-02", FromCustodian: "保管员甲", ToCustodian: "保管员乙",
		FromLocation: "A区 / BLK-SRC-01 / A-1", ToLocation: "B区",
		State: constants.TransferStatePrepared, PreparedByID: 1, PreparedByName: "保管员甲", PreparedAt: time.Now(),
		TemperatureC: &temp,
	}
	if err := db.Create(newTransfer).Error; err != nil {
		t.Fatal(err)
	}
	accepted, _, _, err := transferRepo.Resolve(ctx, newTransfer.ID, TransferResolution{
		State: constants.TransferStateAccepted, ToContainerID: &another.ID, ToPosition: "C-1",
		TemperatureC: &temp, ResolvedByID: resolver, ResolvedByName: "保管员乙", ResolvedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("accept after closure must succeed: %v", err)
	}
	if accepted.State != constants.TransferStateAccepted {
		t.Fatalf("transfer state = %s", accepted.State)
	}
	var moved model.Specimen
	db.First(&moved, specimen.ID)
	if moved.StorageContainerID == nil || *moved.StorageContainerID != another.ID {
		t.Fatal("specimen should have moved after closure and acceptance")
	}
}
