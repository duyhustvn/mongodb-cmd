package snapshot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Collector struct {
	clients  map[string]*mongo.Client
	storage  *Storage
	interval time.Duration
	skipDBs  map[string]bool // DB không monitor (storage DB, system DB...)
}

func NewCollector(clients map[string]*mongo.Client, storage *Storage, intervalMinutes int, skipDB string) *Collector {
	skip := map[string]bool{"admin": true, "local": true, "config": true}
	if skipDB != "" {
		skip[skipDB] = true
	}
	return &Collector{
		clients:  clients,
		storage:  storage,
		interval: time.Duration(intervalMinutes) * time.Minute,
		skipDBs:  skip,
	}
}

// Start khởi động collector.
// - Chưa có snapshot nào → collect ngay lập tức.
// - Có snapshot rồi, lần cuối cách đây >= interval → collect ngay lập tức.
// - Có snapshot rồi, lần cuối cách đây < interval → chờ phần còn lại rồi mới collect.
func (c *Collector) Start(ctx context.Context) {
	go func() {
		endpoints := make([]string, 0, len(c.clients))
		for ep := range c.clients {
			endpoints = append(endpoints, ep)
		}

		firstDelay := c.calcFirstDelay(ctx, endpoints)

		if firstDelay == 0 {
			c.collect(ctx)
		} else {
			log.Printf("[Snapshot] Next collection in %v\n", firstDelay.Round(time.Second))
			select {
			case <-time.After(firstDelay):
				c.collect(ctx)
			case <-ctx.Done():
				return
			}
		}

		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.collect(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// calcFirstDelay tính thời gian chờ trước lần collect đầu tiên.
// Trả về 0 nếu cần collect ngay lập tức.
func (c *Collector) calcFirstDelay(ctx context.Context, endpoints []string) time.Duration {
	latestAt, err := c.storage.FindLatestCapturedAt(ctx, endpoints)
	if err != nil {
		// Chưa có snapshot nào (hoặc lỗi) → collect ngay
		log.Println("[Snapshot] No existing snapshots found, collecting immediately...")
		return 0
	}

	elapsed := time.Since(latestAt)
	if elapsed >= c.interval {
		log.Printf("[Snapshot] Last snapshot was %v ago (>= interval %v), collecting immediately...\n",
			elapsed.Round(time.Second), c.interval)
		return 0
	}

	remaining := c.interval - elapsed
	log.Printf("[Snapshot] Last snapshot was %v ago, next collection in %v\n",
		elapsed.Round(time.Second), remaining.Round(time.Second))
	return remaining
}

func (c *Collector) collect(ctx context.Context) {
	log.Println("[Snapshot] Starting index snapshot collection...")
	for endpoint, client := range c.clients {
		startTime, err := getServerStartTime(ctx, client)
		if err != nil {
			log.Printf("[Snapshot] %s: cannot get server start time: %v\n", endpoint, err)
			continue
		}

		dbNames, err := client.ListDatabaseNames(ctx, bson.M{})
		if err != nil {
			log.Printf("[Snapshot] %s: cannot list databases: %v\n", endpoint, err)
			continue
		}

		for _, dbName := range dbNames {
			if c.skipDBs[dbName] {
				continue
			}
			db := client.Database(dbName)
			colNames, err := db.ListCollectionNames(ctx, bson.M{})
			if err != nil {
				log.Printf("[Snapshot] %s/%s: cannot list collections: %v\n", endpoint, dbName, err)
				continue
			}

			for _, colName := range colNames {
				if strings.HasPrefix(colName, "system.") {
					continue
				}
				entries, err := collectIndexStats(ctx, db.Collection(colName))
				if err != nil {
					log.Printf("[Snapshot] %s/%s/%s: $indexStats error: %v\n", endpoint, dbName, colName, err)
					continue
				}
				snap := IndexSnapshot{
					CapturedAt:      time.Now().UTC(),
					Endpoint:        endpoint,
					Database:        dbName,
					Collection:      colName,
					ServerStartTime: startTime,
					Indexes:         entries,
				}
				saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if err := c.storage.Save(saveCtx, snap); err != nil {
					log.Printf("[Snapshot] %s/%s/%s: save error: %v\n", endpoint, dbName, colName, err)
				}
				cancel()
			}
		}
	}
	log.Println("[Snapshot] Collection completed.")
}

// getServerStartTime tính thời điểm mongod start từ localTime - uptimeMillis.
// Truncate theo phút để tránh drift nhỏ giữa các lần gọi.
func getServerStartTime(ctx context.Context, client *mongo.Client) (time.Time, error) {
	var result bson.M
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}}).Decode(&result); err != nil {
		return time.Time{}, err
	}
	localTime, ok := result["localTime"].(bson.DateTime)
	if !ok {
		return time.Time{}, fmt.Errorf("missing localTime in serverStatus")
	}
	uptimeMillis, ok := result["uptimeMillis"].(int64)
	if !ok {
		return time.Time{}, fmt.Errorf("missing uptimeMillis in serverStatus")
	}
	startTime := localTime.Time().UTC().Add(-time.Duration(uptimeMillis) * time.Millisecond)
	return startTime.Truncate(time.Minute), nil
}

// indexStatDoc là struct decode kết quả từ $indexStats aggregate
type indexStatDoc struct {
	Name     string                 `bson:"name"`
	Key      map[string]interface{} `bson:"key"`
	Accesses struct {
		Ops int64 `bson:"ops"`
	} `bson:"accesses"`
}

func collectIndexStats(ctx context.Context, col *mongo.Collection) ([]IndexEntry, error) {
	cursor, err := col.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$indexStats", Value: bson.D{}}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []indexStatDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	entries := make([]IndexEntry, 0, len(docs))
	for _, d := range docs {
		entries = append(entries, IndexEntry{
			Name: d.Name,
			Key:  d.Key,
			Ops:  d.Accesses.Ops,
		})
	}
	return entries, nil
}
