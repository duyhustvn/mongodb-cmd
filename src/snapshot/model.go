package snapshot

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// IndexEntry là thông tin 1 index tại thời điểm snapshot
type IndexEntry struct {
	Name string                 `bson:"name" json:"name"`
	Key  map[string]interface{} `bson:"key"  json:"key"`
	Ops  int64                  `bson:"ops"  json:"ops"`
}

// IndexSnapshot là 1 bản chụp toàn bộ index stats của 1 collection tại 1 thời điểm
type IndexSnapshot struct {
	ID              primitive.ObjectID `bson:"_id,omitempty"      json:"id"`
	CapturedAt      time.Time          `bson:"captured_at"        json:"captured_at"`
	Endpoint        string             `bson:"endpoint"           json:"endpoint"`
	Database        string             `bson:"database"           json:"database"`
	Collection      string             `bson:"collection"         json:"collection"`
	// ServerStartTime: tính từ localTime - uptimeMillis, truncate theo phút
	// Dùng để phát hiện MongoDB restart giữa 2 snapshot
	ServerStartTime time.Time          `bson:"server_start_time"  json:"server_start_time"`
	Indexes         []IndexEntry       `bson:"indexes"            json:"indexes"`
}

