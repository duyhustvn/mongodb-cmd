package snapshot

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const snapshotCollection = "index_snapshots"

type Storage struct {
	db            *mongo.Database
	retentionDays int // số ngày giữ snapshot, 0 = không TTL
}

func NewStorage(db *mongo.Database, retentionDays int) *Storage {
	return &Storage{db: db, retentionDays: retentionDays}
}

// EnsureIndexes tạo index cho collection lưu snapshot để query nhanh.
// Nếu retentionDays > 0, tạo thêm TTL index trên field captured_at để
// MongoDB tự động xóa document cũ hơn retentionDays ngày.
func (s *Storage) EnsureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		// Compound index cho query nhanh theo endpoint/database/collection/time
		{
			Keys: bson.D{
				{Key: "endpoint", Value: 1},
				{Key: "database", Value: 1},
				{Key: "collection", Value: 1},
				{Key: "captured_at", Value: -1},
			},
		},
	}

	// TTL index: tự động xóa document sau retentionDays ngày
	if s.retentionDays > 0 {
		ttlSeconds := int32(s.retentionDays * 24 * 3600)
		indexes = append(indexes, mongo.IndexModel{
			Keys:    bson.D{{Key: "captured_at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(ttlSeconds),
		})
	}

	_, err := s.db.Collection(snapshotCollection).Indexes().CreateMany(ctx, indexes)
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

// FindLatestCapturedAt trả về thời điểm captured_at của snapshot mới nhất
// trong số các endpoints. Trả về mongo.ErrNoDocuments nếu chưa có snapshot nào.
func (s *Storage) FindLatestCapturedAt(ctx context.Context, endpoints []string) (time.Time, error) {
	opts := options.FindOne().SetSort(bson.D{{Key: "captured_at", Value: -1}})
	var snap IndexSnapshot
	err := s.db.Collection(snapshotCollection).FindOne(ctx, bson.M{
		"endpoint": bson.M{"$in": endpoints},
	}, opts).Decode(&snap)
	if err != nil {
		return time.Time{}, err
	}
	return snap.CapturedAt, nil
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

// FindEarliest tìm snapshot cũ nhất SAU hoặc TẠI thời điểm t
// Dùng làm fallback baseline khi không có snapshot nào trước fromTime
func (s *Storage) FindEarliest(ctx context.Context, endpoint, database, collection string, t time.Time) (*IndexSnapshot, error) {
	filter := bson.M{
		"endpoint":    endpoint,
		"database":    database,
		"collection":  collection,
		"captured_at": bson.M{"$gte": t},
	}
	opts := options.FindOne().SetSort(bson.D{{Key: "captured_at", Value: 1}})

	var snap IndexSnapshot
	err := s.db.Collection(snapshotCollection).FindOne(ctx, filter, opts).Decode(&snap)
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// DeleteBefore xóa tất cả snapshot có captured_at < before.
// endpoint tuỳ chọn: trống = xóa trên tất cả endpoint, có giá trị = chỉ xóa endpoint đó.
// Trả về số document đã xóa.
func (s *Storage) DeleteBefore(ctx context.Context, before time.Time, endpoint string) (int64, error) {
	filter := bson.M{
		"captured_at": bson.M{"$lt": before},
	}
	if endpoint != "" {
		filter["endpoint"] = endpoint
	}
	result, err := s.db.Collection(snapshotCollection).DeleteMany(ctx, filter)
	if err != nil {
		return 0, err
	}
	return result.DeletedCount, nil
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
