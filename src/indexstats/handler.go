package indexstats

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"mongo-profiler/src/snapshot"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	Storage   *snapshot.Storage
	Endpoints []string // danh sách host từ mongodb config, dùng thay vì nhận từ query
}

type indexStatsQuery struct {
	// database, collection tuỳ chọn — bỏ trống = lấy tất cả
	Database   string `form:"database"`
	Collection string `form:"collection"`
	// RFC3339, ví dụ: 2025-01-01T00:00:00Z. Mặc định: from = 24h trước, to = now
	From string `form:"from"`
	To   string `form:"to"`
	// Filter theo số lần sử dụng index (delta): ops_gt=100 → chỉ lấy index dùng > 100 lần
	OpsGt *int64 `form:"ops_gt"`
	OpsLt *int64 `form:"ops_lt"`
}

// GetIndexStats godoc
// @Summary     Thống kê usage của từng index theo khoảng thời gian
// @Description Tính delta số lần sử dụng của mỗi index giữa hai snapshot. Hỗ trợ filter ops_gt/ops_lt để tìm index hot hoặc dead. Trả về single_snapshot=true nếu chỉ có 1 snapshot trong khoảng thời gian.
// @Tags        index
// @Produce     json
// @Param       database    query  string   false  "Lọc theo database"
// @Param       collection  query  string   false  "Lọc theo collection"
// @Param       from        query  string   false  "RFC3339 — mặc định 24h trước (ví dụ: 2025-01-01T00:00:00Z)"
// @Param       to          query  string   false  "RFC3339 — mặc định now"
// @Param       ops_gt      query  integer  false  "Chỉ lấy index có delta > N lần (tìm hot index)"
// @Param       ops_lt      query  integer  false  "Chỉ lấy index có delta < N lần (tìm dead index)"
// @Success     200  {array}   snapshot.DeltaResult
// @Failure     400  {object}  map[string]string
// @Failure     404  {object}  map[string]string
// @Failure     500  {object}  map[string]string
// @Router      /mongodb-cmd/index-stats [get]
func (h *Handler) GetIndexStats(c *gin.Context) {
	var req indexStatsQuery
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	toTime := time.Now().UTC()
	fromTime := toTime.Add(-24 * time.Hour)

	if req.From != "" {
		t, err := time.Parse(time.RFC3339, req.From)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid from (expect RFC3339): %v", err)})
			return
		}
		fromTime = t
	}
	if req.To != "" {
		t, err := time.Parse(time.RFC3339, req.To)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid to (expect RFC3339): %v", err)})
			return
		}
		toTime = t
	}
	if !fromTime.Before(toTime) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from must be before to"})
		return
	}

	ctx := c.Request.Context()

	// Tìm tất cả (endpoint, database, collection) combo có snapshot trước toTime
	targets, err := h.Storage.ListTargets(ctx, h.Endpoints, req.Database, req.Collection, toTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(targets) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no snapshots found — collector may not have run yet"})
		return
	}

	results := make([]snapshot.DeltaResult, 0, len(targets))
	for _, t := range targets {
		snapA, err := h.Storage.FindClosestBefore(ctx, t.Endpoint, t.Database, t.Collection, fromTime)
		if err != nil {
			// Không có snapshot trước fromTime → fallback lấy snapshot cũ nhất sau fromTime làm baseline
			log.Printf("[IndexStats] no snapshot before fromTime for %s/%s/%s, falling back to earliest after fromTime", t.Endpoint, t.Database, t.Collection)
			snapA, err = h.Storage.FindEarliest(ctx, t.Endpoint, t.Database, t.Collection, fromTime)
			if err != nil {
				log.Printf("[IndexStats] no snapshot found at all for %s/%s/%s: %v", t.Endpoint, t.Database, t.Collection, err)
				continue
			}
		}
		snapB, err := h.Storage.FindClosestBefore(ctx, t.Endpoint, t.Database, t.Collection, toTime)
		if err != nil {
			log.Printf("[IndexStats] no snapshot at to for %s/%s/%s: %v", t.Endpoint, t.Database, t.Collection, err)
			continue
		}
		// Cùng 1 snapshot → không có delta, trả về ops tuyệt đối
		if snapA.ID == snapB.ID {
			results = append(results, snapshot.SingleSnapshotResult(snapB))
			continue
		}

		between, err := h.Storage.FindBetween(ctx, t.Endpoint, t.Database, t.Collection, snapA.CapturedAt, snapB.CapturedAt)
		if err != nil {
			log.Printf("[IndexStats] FindBetween error for %s/%s/%s: %v", t.Endpoint, t.Database, t.Collection, err)
			continue
		}

		results = append(results, snapshot.CalcDelta(snapA, snapB, between))
	}

	// Áp dụng filter ops nếu có
	if req.OpsGt != nil || req.OpsLt != nil {
		for i := range results {
			results[i].Indexes = filterIndexes(results[i].Indexes, req.OpsGt, req.OpsLt)
		}
	}

	c.JSON(http.StatusOK, results)
}

// filterIndexes lọc danh sách index theo delta ops
func filterIndexes(indexes []snapshot.IndexDelta, opsGt, opsLt *int64) []snapshot.IndexDelta {
	filtered := make([]snapshot.IndexDelta, 0, len(indexes))
	for _, idx := range indexes {
		if opsGt != nil && idx.Delta <= *opsGt {
			continue
		}
		if opsLt != nil && idx.Delta >= *opsLt {
			continue
		}
		filtered = append(filtered, idx)
	}
	return filtered
}
