package config

import (
	"github.com/xenking/dummypage/api/server"
	"github.com/xenking/dummypage/metrics"

	"github.com/cristalhq/aconfig"
)

type Config struct {
	Server  server.Config
	Log     LogConfig
	Metrics metrics.Config
}

type LogConfig struct {
	Level       string `default:"debug"`
	Filename    string `default:"./app"`
	FileMaxSize int64
}

func LoadEnv(prefix string, configStruct interface{}) error {
	loader := aconfig.LoaderFor(configStruct, aconfig.Config{
		SkipFiles:        true,
		SkipFlags:        true,
		EnvPrefix:        prefix,
		AllowUnknownEnvs: true,
	})
	return loader.Load()
}
