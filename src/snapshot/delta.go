package snapshot

import "time"

// IndexStatus phân loại mức độ sử dụng của index trong khoảng thời gian
type IndexStatus string

const (
	StatusActive IndexStatus = "active"  // delta > 100
	StatusLowUse IndexStatus = "low_use" // 0 < delta <= 100
	StatusDead   IndexStatus = "dead"    // delta == 0
)

// IndexDelta là kết quả so sánh ops của 1 index giữa 2 thời điểm
type IndexDelta struct {
	Name   string                 `json:"name"`
	Key    map[string]interface{} `json:"key"`
	Status IndexStatus            `json:"status"`

	// Ops thực tế tại 2 đầu khoảng (sau khi chọn baseline đúng)
	OpsAtFrom int64 `json:"ops_at_from"`
	OpsAtTo   int64 `json:"ops_at_to"`
	Delta     int64 `json:"delta"`

	// Thông tin restart nếu có
	IsPartial       bool       `json:"is_partial"` // true nếu delta không đầy đủ do restart
	DataLoss        bool       `json:"data_loss"`  // true nếu không có snapshot nào sau restart → không có baseline
	RestartDetected bool       `json:"restart_detected"`
	RestartAt       *time.Time `json:"restart_at,omitempty"` // thời điểm snapshot đầu tiên sau restart
}

// DeltaResult là kết quả trả về cho 1 collection
type DeltaResult struct {
	Endpoint       string       `json:"endpoint"`
	Database       string       `json:"database"`
	Collection     string       `json:"collection"`
	From           time.Time    `json:"from"`                      // captured_at của snapA
	To             time.Time    `json:"to"`                        // captured_at của snapB
	SingleSnapshot bool         `json:"single_snapshot,omitempty"` // true nếu chỉ có 1 snapshot, không có delta
	Indexes        []IndexDelta `json:"indexes"`
}

// SingleSnapshotResult trả về kết quả khi chỉ có 1 snapshot — ops tuyệt đối, không có delta
func SingleSnapshotResult(snap *IndexSnapshot) DeltaResult {
	result := DeltaResult{
		Endpoint:       snap.Endpoint,
		Database:       snap.Database,
		Collection:     snap.Collection,
		From:           snap.CapturedAt,
		To:             snap.CapturedAt,
		SingleSnapshot: true,
	}
	result.Indexes = make([]IndexDelta, 0, len(snap.Indexes))
	for _, idx := range snap.Indexes {
		result.Indexes = append(result.Indexes, IndexDelta{
			Name:    idx.Name,
			Key:     idx.Key,
			OpsAtTo: idx.Ops,
			Delta:   idx.Ops, // ops tuyệt đối từ lúc mongod start
			Status:  classifyIndex(idx.Ops),
		})
	}
	return result
}

// CalcDelta tính delta ops giữa snapA và snapB, xử lý trường hợp MongoDB restart.
//
// between: tất cả snapshot nằm GIỮA snapA và snapB (exclusive cả 2 đầu),
// đã sắp xếp theo captured_at tăng dần — dùng để tìm điểm restart.
func CalcDelta(snapA, snapB *IndexSnapshot, between []IndexSnapshot) DeltaResult {
	result := DeltaResult{
		Endpoint:   snapB.Endpoint,
		Database:   snapB.Database,
		Collection: snapB.Collection,
		From:       snapA.CapturedAt,
		To:         snapB.CapturedAt,
	}

	// Build lookup map ops tại snapA
	opsA := make(map[string]int64, len(snapA.Indexes))
	for _, idx := range snapA.Indexes {
		opsA[idx.Name] = idx.Ops
	}

	// Phát hiện restart: tìm snapshot đầu tiên trong between có ServerStartTime khác snapA
	restartSnap := findFirstRestartSnapshot(snapA.ServerStartTime, between)
	hasRestart := restartSnap != nil || !snapA.ServerStartTime.Equal(snapB.ServerStartTime)

	result.Indexes = make([]IndexDelta, 0, len(snapB.Indexes))
	for _, idx := range snapB.Indexes {
		d := IndexDelta{
			Name:    idx.Name,
			Key:     idx.Key,
			OpsAtTo: idx.Ops,
		}

		if !hasRestart {
			// ✅ Không có restart → delta chính xác hoàn toàn
			d.OpsAtFrom = opsA[idx.Name]
			d.Delta = idx.Ops - opsA[idx.Name]

		} else {
			d.RestartDetected = true
			d.IsPartial = true

			if restartSnap != nil {
				// ✅ Có snapshot ngay sau restart → dùng làm baseline mới
				// Delta = ops_B - ops_tại_snapshot_đầu_tiên_sau_restart
				// Mất data từ A đến lúc restart, nhưng từ restart đến B là chính xác
				baselineOps := int64(0)
				for _, ridx := range restartSnap.Indexes {
					if ridx.Name == idx.Name {
						baselineOps = ridx.Ops
						break
					}
				}
				t := restartSnap.CapturedAt
				d.RestartAt = &t
				d.OpsAtFrom = baselineOps
				d.Delta = idx.Ops - baselineOps

			} else {
				// ⚠️ Restart xảy ra giữa A và B nhưng không có snapshot nào ghi lại
				// → dùng ops_B làm delta tối thiểu (mất hết data trước restart)
				d.DataLoss = true
				d.Delta = idx.Ops
			}
		}

		d.Status = classifyIndex(d.Delta)
		result.Indexes = append(result.Indexes, d)
	}

	return result
}

// findFirstRestartSnapshot tìm snapshot đầu tiên có ServerStartTime khác originalStartTime.
// Vì between đã sort theo captured_at asc, phần tử đầu tiên khác là thời điểm phát hiện restart.
func findFirstRestartSnapshot(originalStartTime time.Time, between []IndexSnapshot) *IndexSnapshot {
	for i := range between {
		if !between[i].ServerStartTime.Equal(originalStartTime) {
			return &between[i]
		}
	}
	return nil
}

func classifyIndex(delta int64) IndexStatus {
	if delta <= 0 {
		return StatusDead
	}
	if delta <= 100 {
		return StatusLowUse
	}
	return StatusActive
}
