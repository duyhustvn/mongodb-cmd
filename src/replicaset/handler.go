package replicaset

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Handler xử lý các request liên quan đến Replica Set.
type Handler struct {
	MongoClients map[string]*mongo.Client
}

type replicaStatusQuery struct {
	Endpoint string `form:"endpoint" binding:"required"`
}

// MemberStatus là trạng thái của 1 member trong replica set.
type MemberStatus struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	Health     float64   `json:"health"`                // 1 = up, 0 = down
	State      int       `json:"state"`                 // 1=PRIMARY, 2=SECONDARY, 7=ARBITER...
	StateStr   string    `json:"state_str"`             // "PRIMARY", "SECONDARY", "RECOVERING"...
	Uptime     int64     `json:"uptime"`                // giây
	OptimeDate time.Time `json:"optime_date"`           // thời điểm áp dụng oplog gần nhất
	LagSeconds *float64  `json:"lag_seconds,omitempty"` // nil nếu là primary
	Self       bool      `json:"self,omitempty"`        // true nếu đây là node đang hỏi
	LastError  string    `json:"last_error,omitempty"`  // lỗi heartbeat nếu có
}

// ReplicaSetStatus là response của API replica-status.
type ReplicaSetStatus struct {
	Endpoint string         `json:"endpoint"`
	Set      string         `json:"set"`      // tên replica set
	Date     time.Time      `json:"date"`     // thời điểm lấy status
	MyState  int            `json:"my_state"` // state của node đang được hỏi
	Members  []MemberStatus `json:"members"`
}

// GetReplicaStatus godoc
// @Summary     Lấy trạng thái Replica Set
// @Description Gọi rs.status() để lấy thông tin tất cả member: state, health, replication lag. lag_seconds=null nếu member là PRIMARY.
// @Tags        replication
// @Produce     json
// @Param       endpoint  query  string  true  "MongoDB host (phải là member của replica set)"
// @Success     200  {object}  ReplicaSetStatus
// @Failure     400  {object}  map[string]string
// @Failure     500  {object}  map[string]string
// @Router      /mongodb-cmd/replica-status [get]
func (h *Handler) GetReplicaStatus(c *gin.Context) {
	var req replicaStatusQuery
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, exists := h.MongoClients[req.Endpoint]
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "endpoint not found"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	var raw bson.M
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&raw); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Parse tên set, date, myState
	setName, _ := raw["set"].(string)
	myState := int(toInt64(raw["myState"]))
	serverDate, _ := raw["date"].(bson.DateTime)
	date := serverDate.Time().UTC()

	// Tìm optimeDate của PRIMARY để tính lag
	members, _ := raw["members"].(bson.A)
	var primaryOptimeDate *time.Time
	for _, m := range members {
		mm, ok := m.(bson.M)
		if !ok {
			continue
		}
		if int(toInt64(mm["state"])) == 1 { // PRIMARY
			if od, ok := mm["optimeDate"].(bson.DateTime); ok {
				t := od.Time().UTC()
				primaryOptimeDate = &t
			}
			break
		}
	}

	result := ReplicaSetStatus{
		Endpoint: req.Endpoint,
		Set:      setName,
		Date:     date,
		MyState:  myState,
		Members:  make([]MemberStatus, 0, len(members)),
	}

	for _, m := range members {
		mm, ok := m.(bson.M)
		if !ok {
			continue
		}
		state := int(toInt64(mm["state"]))
		optimeDateRaw, _ := mm["optimeDate"].(bson.DateTime)
		optimeDate := optimeDateRaw.Time().UTC()

		ms := MemberStatus{
			ID:         int(toInt64(mm["_id"])),
			Name:       stringField(mm, "name"),
			Health:     toFloat64(mm["health"]),
			State:      state,
			StateStr:   stringField(mm, "stateStr"),
			Uptime:     toInt64(mm["uptime"]),
			OptimeDate: optimeDate,
			Self:       boolField(mm, "self"),
			LastError:  stringField(mm, "lastHeartbeatMessage"),
		}

		// Tính lag so với PRIMARY (chỉ cho SECONDARY và các state không phải PRIMARY)
		if state != 1 && primaryOptimeDate != nil && !optimeDate.IsZero() {
			lag := primaryOptimeDate.Sub(optimeDate).Seconds()
			ms.LagSeconds = &lag
		}

		result.Members = append(result.Members, ms)
	}

	c.JSON(http.StatusOK, result)
}

func stringField(m bson.M, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolField(m bson.M, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int32:
		return int64(val)
	case int64:
		return val
	case float64:
		return int64(val)
	}
	return 0
}

func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int32:
		return float64(val)
	case int64:
		return float64(val)
	}
	return 0
}
