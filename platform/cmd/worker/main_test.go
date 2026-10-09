package main

import (
	"strings"
	"testing"

	"github.com/yuqing/platform/internal/config"
)

func TestWorkerRefusesUnwiredQueue(t *testing.T) {
	for _, testCase := range []struct{ store, driver, want string }{
		{"postgres", "memory", "persistent PostgreSQL queue"},
		{"postgres", "postgres", "engines.query.url"},
		{"memory", "redis", "unsupported queue driver"},
		{"memory", "memory", "requires PostgreSQL"},
	} {
		t.Run(testCase.store+"/"+testCase.driver, func(t *testing.T) {
			err := runWorker(&config.Config{Store: config.StoreConfig{Driver: testCase.store}, Queue: config.QueueConfig{Driver: testCase.driver}})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("runWorker err=%v, want %q", err, testCase.want)
			}
		})
	}
}
