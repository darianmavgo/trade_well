package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppKey            string
	AppSecret         string
	CallbackURL       string
	TokenPath         string
	RothAccountNumber string
	DryRunDefault     bool
	Debug             bool
	LogFile           string
	BaseURL           string
}

// Load builds the Config from environment variables. The environment is filled in
// once, at process start, by Bootstrap (from the GCS config object); Load itself reads
// nothing but the environment, so it is safe and free to call anywhere, tests included.
func Load() *Config {
	cfg := &Config{
		AppKey:            os.Getenv("SCHWAB_APP_KEY"),
		AppSecret:         os.Getenv("SCHWAB_APP_SECRET"),
		CallbackURL:       getEnvDefault("SCHWAB_CALLBACK_URL", "https://127.0.0.1"),
		TokenPath:         getEnvDefault("SCHWAB_TOKEN_PATH", "token.json"),
		RothAccountNumber: os.Getenv("SCHWAB_ROTH_ACCOUNT_NUMBER"),
		DryRunDefault:     getEnvBool("DRY_RUN_DEFAULT", true),
		Debug:             getEnvBool("SCHWAB_DEBUG", false),
		LogFile:           getEnvDefault("SCHWAB_LOG_FILE", "schwab_api.log"),
		BaseURL:           getEnvDefault("SCHWAB_BASE_URL", ""),
	}

	return cfg
}

func getEnvDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvFloat(key string, defaultVal float64) float64 {
	if val := os.Getenv(key); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		val = strings.ToLower(val)
		return val == "true" || val == "1" || val == "yes"
	}
	return defaultVal
}
