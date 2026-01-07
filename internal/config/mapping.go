package config

import (
	"log"
	"os"
	"strings"
	"sync"
)

var (
	modelMapping  map[string]string
	mappingMu     sync.RWMutex
	mappingLoaded bool
)

func LoadModelMapping() {
	mappingMu.Lock()
	defer mappingMu.Unlock()

	modelMapping = make(map[string]string)

	mappingStr := os.Getenv("MODEL_MAPPING")
	if mappingStr == "" {
		return
	}

	pairs := strings.Split(mappingStr, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 {
			source := strings.TrimSpace(parts[0])
			target := strings.TrimSpace(parts[1])
			if source != "" && target != "" {
				modelMapping[source] = target
				log.Printf("[Config] Model mapping: %s -> %s", source, target)
			}
		}
	}
	mappingLoaded = true
}

func MapModel(model string) string {
	mappingMu.RLock()
	defer mappingMu.RUnlock()

	if !mappingLoaded {
		mappingMu.RUnlock()
		LoadModelMapping()
		mappingMu.RLock()
	}

	if mapped, ok := modelMapping[model]; ok {
		return mapped
	}
	return model
}
