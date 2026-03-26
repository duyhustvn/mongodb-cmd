package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mongo-profiler/src/config"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func NewMongodbConfig(config *config.Config) []string {
	host := config.Mongodb.Host
	username := config.Mongodb.Username
	password := config.Mongodb.Password

	if host == "" || username == "" || password == "" {
		log.Fatal("Missing required MongoDB environment variables")
	}

	var urls []string
	hosts := strings.Split(host, ",")
	for _, h := range hosts {
		trimmed := strings.TrimSpace(h)
		if trimmed == "" {
			continue
		}
		// encode username/password
		encodedUser := url.QueryEscape(username)
		encodedPass := url.QueryEscape(password)

		uri := fmt.Sprintf("mongodb://%s:%s@%s/?directConnection=true", encodedUser, encodedPass, trimmed)
		urls = append(urls, uri)
	}

	return urls
}

type handler struct {
	Config *config.Config

	MongoClients map[string]*mongo.Client
}

func NewHandler(cfg *config.Config) *handler {
	mongoUrls := NewMongodbConfig(cfg)
	clients := make(map[string]*mongo.Client)

	// Tạo context dùng một lần để setup toàn bộ kết nối ban đầu
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, u := range mongoUrls {
		fmt.Println("mongourl: ", u)
		parsed, err := url.Parse(u)
		if err != nil {
			log.Printf("Invalid MongoDB URL format: %v\n", err)
			continue
		}
		host := parsed.Hostname()

		clientOpts := options.Client().ApplyURI(u)
		client, err := mongo.Connect(ctx, clientOpts)
		if err != nil {
			log.Printf("Failed to create client for %s: %v\n", host, err)
			panic(err)
		}

		if err := client.Ping(ctx, nil); err != nil {
			log.Printf("Failed to ping %s: %v\n", host, err)
			panic(err)
		}

		log.Printf("Successfully connected to MongoDB: %s\n", host)
		clients[host] = client // Lưu vào map
	}

	return &handler{
		Config:       cfg,
		MongoClients: clients,
	}
}

type CountSystemProfileRequest struct {
	Databases string `form:"databases"`
}

type CountSystemProfileResult struct {
	URL      string  `json:"url"`
	Database string  `json:"database"`
	Count    int64   `json:"count"`
	Error    *string `json:"error,omitempty"`
}

type CountSystemProfileResponse struct {
	Results []CountSystemProfileResult `json:"results"`
}

func (inst *handler) GetProfiles(c *gin.Context) {
	var query CountSystemProfileRequest
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var results []CountSystemProfileResult = make([]CountSystemProfileResult, 0)

	// 1. Phân tách danh sách databases từ user (nếu có)
	var userDBs []string
	if query.Databases != "" {
		for _, db := range strings.Split(query.Databases, ",") {
			trimmed := strings.TrimSpace(db)
			if trimmed != "" {
				userDBs = append(userDBs, trimmed)
			}
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	for host, client := range inst.MongoClients {
		targetDBs := userDBs

		if len(targetDBs) == 0 {
			dbNames, err := client.ListDatabaseNames(ctx, bson.M{})
			if err != nil {
				errMsg := err.Error()
				results = append(results, CountSystemProfileResult{
					URL:      host,
					Database: "",
					Count:    0,
					Error:    &errMsg,
				})
				continue // Chuyển sang server tiếp theo
			}
			targetDBs = dbNames
		}

		for _, dbName := range targetDBs {
			col := client.Database(dbName).Collection("system.profile")
			count, err := col.CountDocuments(ctx, bson.M{})

			if err != nil {
				errMsg := err.Error()
				results = append(results, CountSystemProfileResult{
					URL:      host,
					Database: dbName,
					Count:    0,
					Error:    &errMsg,
				})
			} else {
				results = append(results, CountSystemProfileResult{
					URL:      host,
					Database: dbName,
					Count:    count,
				})
			}
		}
	}

	c.JSON(http.StatusOK, CountSystemProfileResponse{Results: results})
}

type GetDatabaseProfileQuery struct {
	Endpoint   string `form:"endpoint" binding:"required"`
	Database   string `form:"database" binding:"required"`
	Limit      *int64 `form:"limit"`
	Offset     *int64 `form:"offset"`
	Collection string `form:"collection"`
	Duration   *int64 `form:"duration"`
	OrderBy    string `form:"order_by"`
	OrderType  string `form:"order_type"` // "asc" hoặc "desc"
	Unique     *bool  `form:"unique"`
}

func (inst *handler) GetProfile(c *gin.Context) {
	var query GetDatabaseProfileQuery

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, exists := inst.MongoClients[query.Endpoint]
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Endpoint %s not found or disconnected", query.Endpoint)})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	col := client.Database(query.Database).Collection("system.profile")

	filter := bson.M{} // Dùng bson.M cho Map không cần thứ tự
	if query.Collection != "" {
		filter["ns"] = fmt.Sprintf("%s.%s", query.Database, query.Collection)
	}
	if query.Duration != nil {
		filter["millis"] = bson.M{"$gt": *query.Duration}
	}

	limit := int64(20)
	if query.Limit != nil {
		limit = *query.Limit
	}
	offset := int64(0)
	if query.Offset != nil {
		offset = *query.Offset
	}

	isUnique := false
	if query.Unique != nil && *query.Unique {
		isUnique = true
	}

	if isUnique {
		// Dùng bson.D cho các operations yêu cầu tính thứ tự (như Pipeline)
		pipeline := mongo.Pipeline{
			{{Key: "$match", Value: filter}},
			{{Key: "$group", Value: bson.D{
				{Key: "_id", Value: "$queryHash"},
				{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
				{Key: "max_millis", Value: bson.D{{Key: "$max", Value: "$millis"}}},
				{Key: "avg_millis", Value: bson.D{{Key: "$avg", Value: "$millis"}}},
				{Key: "latest_ts", Value: bson.D{{Key: "$max", Value: "$ts"}}},
				{Key: "sample_ns", Value: bson.D{{Key: "$first", Value: "$ns"}}},
			}}},
			{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}}}}, // Xếp theo query xuất hiện nhiều nhất
			{{Key: "$skip", Value: offset}},
			{{Key: "$limit", Value: limit}},
		}

		cursor, err := col.Aggregate(ctx, pipeline)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		var results []bson.M
		if err = cursor.All(ctx, &results); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if results == nil {
			results = []bson.M{}
		}
		c.JSON(http.StatusOK, results)

	} else {
		sortField := "ts"
		if query.OrderBy != "" {
			sortField = query.OrderBy
		}

		sortDirection := -1
		if query.OrderType == "asc" {
			sortDirection = 1
		}

		findOptions := options.Find().
			SetSort(bson.D{{Key: sortField, Value: sortDirection}}).
			SetLimit(limit).
			SetSkip(offset)

		cursor, err := col.Find(ctx, filter, findOptions)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		var results []bson.M
		if err = cursor.All(ctx, &results); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if results == nil {
			results = []bson.M{}
		}
		c.JSON(http.StatusOK, results)
	}
}

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	handler := NewHandler(cfg)

	r := gin.Default()

	r.GET("/mongodb-cmd/profiles", handler.GetProfiles)
	r.GET("/mongodb-cmd/profile", handler.GetProfile)

	fmt.Printf("Server running on :%d", cfg.Server.Port)

	if err := r.Run(fmt.Sprintf(":%d", cfg.Server.Port)); err != nil {
		log.Fatal(err)
	}
}
