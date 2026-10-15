package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

const dotenvPath = ".env"

// Load 从环境文件与操作系统环境变量装载并校验全部应用配置。
func Load() (*Config, error) {
	if err := godotenv.Load(dotenvPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: load %s failed: %w", dotenvPath, err)
		}
	}

	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config: parse failed: %w", err)
	}

	cfg.Database.Debug = cfg.ResolveDBDebug()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Default 返回一份最小可用默认配置，供配置装载失败时的路由自省类命令使用。
func Default() *Config {
	return &Config{
		App: AppConfig{
			Name: "bfiber",
			Env:  "local",
		},
	}
}
