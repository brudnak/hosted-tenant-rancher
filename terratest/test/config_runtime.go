package test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/viper"
)

func setupConfig(tb testing.TB) {
	tb.Helper()
	if err := ensureConfigLoaded(); err != nil {
		tb.Fatalf("failed to read config: %v", err)
	}
}

func ensureConfigLoaded() error {
	if viper.ConfigFileUsed() != "" {
		return nil
	}

	viper.SetConfigType("yml")
	viper.AddConfigPath("../../")

	for _, configName := range []string{"tool-config", "config"} {
		viper.SetConfigName(configName)
		if err := viper.ReadInConfig(); err == nil {
			return nil
		} else {
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) {
				return err
			}
		}
	}

	return fmt.Errorf("expected tool-config.yml at the repo root")
}

func getTotalRancherInstances() int {
	if total := viper.GetInt("total_rancher_instances"); total > 0 {
		return total
	}
	return viper.GetInt("total_has")
}
