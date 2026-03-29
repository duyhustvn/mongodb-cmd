package currentop

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type Handler struct {
	MongoClients map[string]*mongo.Client
}

type currentOpQuery struct {
	Endpoint string `form:"endpoint" binding:"required"`
	// Chỉ lấy op chạy lâu hơn N giây — giảm payload, giảm tác động lên hệ thống
	MinDurationSecs *int `form:"min_duration_secs"`
	// Filter tuỳ chọn
	Collection string `form:"collection"`
	OpType     string `form:"op_type"` // query, update, insert, delete, command, ...
}

// CurrentOp là thông tin của 1 operation đang chạy
type CurrentOp struct {
	OpID             interface{} `json:"op_id"`
	Type             string      `json:"type"`
	Namespace        string      `json:"namespace"`
	SecsRunning      int64       `json:"secs_running"`
	MicrosecsRunning int64       `json:"microsecs_running"`
	PlanSummary      string      `json:"plan_summary"`
	WaitingForLock   bool        `json:"waiting_for_lock"`
	NumYields        int64       `json:"num_yields"`
	Command          interface{} `json:"command"`
	Client           string      `json:"client"`
}

/*
# Tất cả op đang chạy trên endpoint
GET /mongodb-cmd/current-ops?endpoint=172.17.0.1

# Chỉ op chạy hơn 5 giây (an toàn khi perf test — payload nhỏ)
GET /mongodb-cmd/current-ops?endpoint=172.17.0.1&min_duration_secs=5

# Lọc theo collection + op type
GET /mongodb-cmd/current-ops?endpoint=172.17.0.1&min_duration_secs=1&collection=orders&op_type=query
*/
func (h *Handler) GetCurrentOps(c *gin.Context) {
	var req currentOpQuery
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, exists := h.MongoClients[req.Endpoint]
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "endpoint not found"})
		return
	}

	// Build filter truyền thẳng vào lệnh currentOp → MongoDB lọc server-side, không kéo toàn bộ về
	filter := bson.D{}
	if req.MinDurationSecs != nil {
		filter = append(filter, bson.E{Key: "secs_running", Value: bson.M{"$gte": *req.MinDurationSecs}})
	}
	if req.Collection != "" {
		filter = append(filter, bson.E{Key: "ns", Value: bson.M{"$regex": req.Collection}})
	}
	if req.OpType != "" {
		filter = append(filter, bson.E{Key: "op", Value: req.OpType})
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	cmd := bson.D{
		{Key: "currentOp", Value: 1},
		{Key: "filter", Value: filter},
	}

	var result bson.M
	if err := client.Database("admin").RunCommand(ctx, cmd).Decode(&result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Parse inprog array
	inprog, _ := result["inprog"].(bson.A)
	ops := make([]CurrentOp, 0, len(inprog))
	for _, item := range inprog {
		raw, ok := item.(bson.M)
		if !ok {
			continue
		}
		op := CurrentOp{
			OpID:             raw["opid"],
			Type:             stringField(raw, "op"),
			Namespace:        stringField(raw, "ns"),
			SecsRunning:      int64Field(raw, "secs_running"),
			MicrosecsRunning: int64Field(raw, "microsecs_running"),
			PlanSummary:      stringField(raw, "planSummary"),
			WaitingForLock:   boolField(raw, "waitingForLock"),
			NumYields:        int64Field(raw, "numYields"),
			Command:          raw["command"],
			Client:           stringField(raw, "client"),
		}
		ops = append(ops, op)
	}

	c.JSON(http.StatusOK, gin.H{
		"endpoint": req.Endpoint,
		"count":    len(ops),
		"ops":      ops,
	})
}

func stringField(m bson.M, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolField(m bson.M, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func int64Field(m bson.M, key string) int64 {
	switch v := m[key].(type) {
	case int32:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}
