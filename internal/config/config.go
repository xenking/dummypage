package config

import (
	"github.com/cristalhq/aconfig"

	"github.com/xenking/dummypage/internal/server"
)

type Config struct {
	Server server.Config
	Log    LogConfig
}

type LogConfig struct {
	Level string `default:"debug"`
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
