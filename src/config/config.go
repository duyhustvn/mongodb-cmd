package config

import (
	"strings"

	"github.com/spf13/viper"
)

type ServerConfig struct {
	Port int `mapstructure:"port"`
}

// StorageConfig: cấu hình DB lưu snapshot.
// Nếu URI được cung cấp thì dùng URI đó, ngược lại dùng lại cụm mongodb đã định nghĩa.
type StorageConfig struct {
	URI      string `mapstructure:"uri"`
	Database string `mapstructure:"database"`
}

// SnapshotConfig: cấu hình background collector
type SnapshotConfig struct {
	IntervalMinutes int `mapstructure:"interval_minutes"`
	// RetentionDays: số ngày giữ snapshot, snapshot cũ hơn sẽ tự động bị xóa bởi TTL index.
	// Mặc định 90 ngày. Đặt 0 để tắt TTL.
	RetentionDays int `mapstructure:"retention_days"`
}

type Config struct {
	Mongodb  MongodbConfig  `mapstructure:"mongodb"`
	Storage  StorageConfig  `mapstructure:"storage"`
	Snapshot SnapshotConfig `mapstructure:"snapshot"`
	Server   ServerConfig   `mapstructure:"server"`
}

func LoadConfig() (config *Config, err error) {
	v := viper.New()

	v.SetDefault("server.port", 8082)

	v.SetDefault("mongodb.host", "localhost:27017")
	v.SetDefault("mongodb.username", "admin")
	v.SetDefault("mongodb.password", "changeme")

	v.SetDefault("storage.uri", "")
	v.SetDefault("storage.database", "mongo_monitoring")
	v.SetDefault("snapshot.interval_minutes", 60)
	v.SetDefault("snapshot.retention_days", 90)

	// config read from yaml
	v.AddConfigPath(".") // search at this directory
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	// Config read from env
	v.AutomaticEnv()
	// Change "." in struct to "_" in ENV (server.port -> SERVER_PORT)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err = v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// error is not "file not found"
			return
		}
	}

	// load data to struct
	err = v.Unmarshal(&config)
	return
}
