package config

import (
	"strings"

	"github.com/spf13/viper"
)

type ServerConfig struct {
	Port int `mapstructure:"port"`
}

type Config struct {
	Mongodb MongodbConfig `mapstructure:"mongodb"`
	Server  ServerConfig  `mapstructure:"server"`
}

func LoadConfig() (config *Config, err error) {
	v := viper.New()

	v.SetDefault("server.port", 8082)

	v.SetDefault("mongo.host", "localhost:27017")
	v.SetDefault("mongo.username", "admin")
	v.SetDefault("mongo.password", "password")

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
