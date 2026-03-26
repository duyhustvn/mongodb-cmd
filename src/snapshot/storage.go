package snapshot

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const snapshotCollection = "index_snapshots"

type Storage struct {
	db *mongo.Database
}

func NewStorage(db *mongo.Database) *Storage {
	return &Storage{db: db}
}

// EnsureIndexes tạo index cho collection lưu snapshot để query nhanh
func (s *Storage) EnsureIndexes(ctx context.Context) error {
	_, err := s.db.Collection(snapshotCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "endpoint", Value: 1},
			{Key: "database", Value: 1},
			{Key: "collection", Value: 1},
			{Key: "captured_at", Value: -1},
		},
	})
	return err
}

func (s *Storage) Save(ctx context.Context, snap IndexSnapshot) error {
	_, err := s.db.Collection(snapshotCollection).InsertOne(ctx, snap)
	return err
}

// HasAnySnapshot kiểm tra đã có snapshot nào cho các endpoint này chưa
func (s *Storage) HasAnySnapshot(ctx context.Context, endpoints []string) (bool, error) {
	count, err := s.db.Collection(snapshotCollection).CountDocuments(ctx, bson.M{
		"endpoint": bson.M{"$in": endpoints},
	}, options.Count().SetLimit(1))
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// FindClosestBefore tìm snapshot gần nhất TRƯỚC hoặc TẠI thời điểm t
func (s *Storage) FindClosestBefore(ctx context.Context, endpoint, database, collection string, t time.Time) (*IndexSnapshot, error) {
	filter := bson.M{
		"endpoint":    endpoint,
		"database":    database,
		"collection":  collection,
		"captured_at": bson.M{"$lte": t},
	}
	opts := options.FindOne().SetSort(bson.D{{Key: "captured_at", Value: -1}})

	var snap IndexSnapshot
	err := s.db.Collection(snapshotCollection).FindOne(ctx, filter, opts).Decode(&snap)
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// Target đại diện cho 1 tổ hợp (endpoint, database, collection) duy nhất
type Target struct {
	Endpoint   string
	Database   string
	Collection string
}

// ListTargets trả về tất cả (endpoint, database, collection) combo có ít nhất 1 snapshot trước `before`.
// endpoints: chỉ lấy trong danh sách này (bắt buộc, lấy từ config).
// database, collection: filter tuỳ chọn, trống = lấy tất cả.
func (s *Storage) ListTargets(ctx context.Context, endpoints []string, database, collection string, before time.Time) ([]Target, error) {
	match := bson.M{
		"endpoint":    bson.M{"$in": endpoints},
		"captured_at": bson.M{"$lte": before},
	}
	if database != "" {
		match["database"] = database
	}
	if collection != "" {
		match["collection"] = collection
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{
				{Key: "endpoint", Value: "$endpoint"},
				{Key: "database", Value: "$database"},
				{Key: "collection", Value: "$collection"},
			}},
		}}},
	}

	cursor, err := s.db.Collection(snapshotCollection).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var rows []struct {
		ID struct {
			Endpoint   string `bson:"endpoint"`
			Database   string `bson:"database"`
			Collection string `bson:"collection"`
		} `bson:"_id"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}

	targets := make([]Target, 0, len(rows))
	for _, r := range rows {
		targets = append(targets, Target{
			Endpoint:   r.ID.Endpoint,
			Database:   r.ID.Database,
			Collection: r.ID.Collection,
		})
	}
	return targets, nil
}

// FindBetween lấy tất cả snapshot nằm GIỮA 2 thời điểm (exclusive cả 2 đầu)
// Dùng để phát hiện restart xảy ra giữa snapA và snapB
func (s *Storage) FindBetween(ctx context.Context, endpoint, database, collection string, after, before time.Time) ([]IndexSnapshot, error) {
	filter := bson.M{
		"endpoint":   endpoint,
		"database":   database,
		"collection": collection,
		"captured_at": bson.M{
			"$gt": after,
			"$lt": before,
		},
	}
	opts := options.Find().SetSort(bson.D{{Key: "captured_at", Value: 1}})

	cursor, err := s.db.Collection(snapshotCollection).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var snaps []IndexSnapshot
	if err := cursor.All(ctx, &snaps); err != nil {
		return nil, err
	}
	return snaps, nil
}
